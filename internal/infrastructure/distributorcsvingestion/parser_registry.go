package distributorcsvingestion

import (
	"fmt"

	ingapp "clinic-inventory-csv-functions/internal/application/distributorcsvingestion"
)

// ParseFunc は「CSVの中身 → 中間表現」の変換1つ分。卸ごとに1つ書く（catalog_*.go）。
// これがそのまま CatalogCsvParser インターフェースを満たすので、卸別パーサは
// 構造体を作らず関数だけで済む。
type ParseFunc func(body []byte) ([]ingapp.CatalogRow, error)

func (f ParseFunc) Parse(body []byte) ([]ingapp.CatalogRow, error) { return f(body) }

// ParserRegistry は卸コードと卸別パーサの対応表。
//
// **卸ごとに違うのはここまで**で、中間表現より後ろ（ステージング保存・DB反映）は
// 全卸で共通のコードが動く。新しい卸に対応するときは、catalog_<卸コード>.go を1つ書いて
// この表に1行足す。
type ParserRegistry struct {
	parsers map[string]ParseFunc
}

func NewParserRegistry(parsers map[string]ParseFunc) *ParserRegistry {
	return &ParserRegistry{parsers: parsers}
}

// DefaultParsers は本番で使う対応表。卸コードは backend の distributors.code。
func DefaultParsers() *ParserRegistry {
	return NewParserRegistry(map[string]ParseFunc{
		"sample-pharma": ParseSamplePharmaCatalogCSV, // 商品ごとの単価（UTF-8）
		"oroshi-a":      ParseOroshiACatalogCSV,      // 商品ごとの単価（Shift_JIS）
		"oroshi-b":      ParseOroshiBCatalogCSV,      // 医院ごとの単価（1商品×1医院で1行）
	})
}

func (r *ParserRegistry) Resolve(distributorCode string) (ingapp.CatalogCsvParser, error) {
	parse, ok := r.parsers[distributorCode]
	if !ok {
		return nil, fmt.Errorf("卸コード %s のCSVパーサがありません。catalog_%s.go を作って DefaultParsers に登録してください", distributorCode, distributorCode)
	}
	return parse, nil
}

// DistributorCodes は対応済みの卸コードを返す（運用確認用）。
func (r *ParserRegistry) DistributorCodes() []string {
	codes := make([]string, 0, len(r.parsers))
	for code := range r.parsers {
		codes = append(codes, code)
	}
	return codes
}
