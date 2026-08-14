package distributorcsvingestion

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	ingapp "clinic-inventory-csv-functions/internal/application/distributorcsvingestion"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"

	"github.com/google/uuid"
)

// ColumnMapping は1社分のCSV読み取り定義
// (docs/design.md「卸ごとのフォーマット差」)。
// 卸ごとの違いを、まずはこの定義だけで吸収する。定義で表せない構造の卸が出てきたら、
// その卸だけ専用のCatalogCsvParser実装を足す。
type ColumnMapping struct {
	// Encoding は "shift_jis" または "utf8"(既定)。国内の卸CSVはShift_JISのことが多い。
	Encoding  string `json:"encoding"`
	HasHeader bool   `json:"hasHeader"`
	// DefaultVendorName はCSVにベンダー(メーカー)名の列が無い卸向けの既定値。
	// ベンダー名は卸商品の必須項目のため、列が無い場合はここで補う。
	DefaultVendorName string `json:"defaultVendorName"`
	// DiscontinuedTrueValue は廃盤列がこの値と一致したら廃盤とみなす（既定 "1"）。
	DiscontinuedTrueValue string        `json:"discontinuedTrueValue"`
	Columns               ColumnIndexes `json:"columns"`
}

// ColumnIndexes は各項目が何列目か（0始まり）。未指定(nil)の項目は取り込まない。
type ColumnIndexes struct {
	DistributorProductCode *int `json:"distributorProductCode"`
	Name                   *int `json:"name"`
	VendorName             *int `json:"vendorName"`
	VendorProductCode      *int `json:"vendorProductCode"`
	JANCode                *int `json:"janCode"`
	UnitPrice              *int `json:"unitPrice"`
	Discontinued           *int `json:"discontinued"`
	// FacilityCode/FacilityUnitPrice を指定すると「医院別単価CSV」として読む。
	// 1商品×1医院で1行になるため、同じ卸商品コードの行をまとめて1件の商品にする。
	FacilityCode      *int `json:"facilityCode"`
	FacilityUnitPrice *int `json:"facilityUnitPrice"`
}

// MappingParserResolver は卸IDに対応するパーサを返す。マッピング定義はJSONファイルで持つ。
// 卸IDは実行時に採番されるUUIDのためソースに埋め込まず、設定ファイルに外出ししている。
type MappingParserResolver struct {
	mappings map[uuid.UUID]ColumnMapping
}

func NewMappingParserResolver(mappings map[uuid.UUID]ColumnMapping) *MappingParserResolver {
	return &MappingParserResolver{mappings: mappings}
}

// LoadMappings は設定ファイルを読み込む。形式は
// { "<卸ID(UUID)>": { "encoding": ..., "columns": {...} }, ... }
func LoadMappings(path string) (map[uuid.UUID]ColumnMapping, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("CSVマッピング定義の読み込みに失敗しました(%s): %w", path, err)
	}
	// 注記("_"始まりのキー)を読み飛ばしてから各定義を解釈するため、いったん生のJSONで受ける。
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("CSVマッピング定義の形式が不正です(%s): %w", path, err)
	}
	mappings := make(map[uuid.UUID]ColumnMapping, len(raw))
	for key, value := range raw {
		// JSONにはコメント構文が無いため、"_"始まりのキーを注記として扱う。
		if strings.HasPrefix(key, "_") {
			continue
		}
		id, err := uuid.Parse(key)
		if err != nil {
			return nil, fmt.Errorf("CSVマッピング定義のキーが卸ID(UUID)ではありません: %s", key)
		}
		var mapping ColumnMapping
		if err := json.Unmarshal(value, &mapping); err != nil {
			return nil, fmt.Errorf("卸業者 %s のCSVマッピング定義の形式が不正です: %w", key, err)
		}
		mappings[id] = mapping
	}
	return mappings, nil
}

func (r *MappingParserResolver) Resolve(distributorID shareddomain.ID) (ingapp.CatalogCsvParser, error) {
	mapping, ok := r.mappings[uuid.UUID(distributorID)]
	if !ok {
		return nil, fmt.Errorf("卸業者 %s のCSVマッピング定義がありません。設定ファイルに追加してください", distributorID)
	}
	return NewMappingCsvParser(mapping), nil
}

// DistributorIDs は定義済みの卸IDを返す（取り込み対象のプレフィックスを決めるのに使う）。
func (r *MappingParserResolver) DistributorIDs() []shareddomain.ID {
	ids := make([]shareddomain.ID, 0, len(r.mappings))
	for id := range r.mappings {
		ids = append(ids, shareddomain.ID(id))
	}
	return ids
}
