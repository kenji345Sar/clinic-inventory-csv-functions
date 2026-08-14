package distributorcsvingestion

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	ingapp "clinic-inventory-csv-functions/internal/application/distributorcsvingestion"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// MappingCsvParser は列マッピング定義に従ってCSVを中間表現へ変換する汎用パーサ。
// 卸ごとに違うのは「どの列が何か」「文字コード」「ヘッダの有無」だけ、という前提で作っている。
type MappingCsvParser struct {
	mapping ColumnMapping
}

func NewMappingCsvParser(mapping ColumnMapping) *MappingCsvParser {
	return &MappingCsvParser{mapping: mapping}
}

// Parse はCSV全体を読み、1行ずつ中間表現に変換する。
// 読み取れない行はエラーにせず ErrorMessage を付けて返し、最後まで読み切る
// (1行の不備でファイル全体が見えなくなるのを避ける)。
// ファイル全体が読めない場合(文字コード不正など)だけerrorを返す。
func (p *MappingCsvParser) Parse(body []byte) ([]ingapp.CatalogRow, error) {
	decoded, err := decode(body, p.mapping.Encoding)
	if err != nil {
		return nil, err
	}

	reader := csv.NewReader(bytes.NewReader(decoded))
	// 卸のCSVは行によって列数が違うことがあるため、列数の一致チェックはしない
	// (必要な列が取れるかは行ごとに判定する)。
	reader.FieldsPerRecord = -1

	rows := make([]ingapp.CatalogRow, 0)
	rowNo := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		rowNo++
		if err != nil {
			return nil, fmt.Errorf("%d行目でCSVの読み取りに失敗しました: %w", rowNo, err)
		}
		if rowNo == 1 && p.mapping.HasHeader {
			continue
		}
		if isEmptyRecord(record) {
			continue
		}
		rows = append(rows, p.toRow(rowNo, record))
	}

	if p.isFacilityPriceFormat() {
		return groupByProductCode(rows), nil
	}
	return rows, nil
}

func (p *MappingCsvParser) toRow(rowNo int, record []string) ingapp.CatalogRow {
	raw := strings.Join(record, ",")
	row := ingapp.CatalogRow{RowNo: rowNo, Raw: raw}

	code, ok := column(record, p.mapping.Columns.DistributorProductCode)
	if !ok || code == "" {
		row.ErrorMessage = "卸商品コードが取得できません"
		return row
	}
	row.DistributorProductCode = code

	name, _ := column(record, p.mapping.Columns.Name)
	if name == "" {
		row.ErrorMessage = "商品名が取得できません"
		return row
	}
	row.Name = name

	vendorName, _ := column(record, p.mapping.Columns.VendorName)
	if vendorName == "" {
		vendorName = p.mapping.DefaultVendorName
	}
	if vendorName == "" {
		row.ErrorMessage = "ベンダー名が取得できません（CSVに列が無い場合は設定のdefaultVendorNameで補ってください）"
		return row
	}
	row.VendorName = vendorName

	row.VendorProductCode, _ = column(record, p.mapping.Columns.VendorProductCode)
	row.JANCode, _ = column(record, p.mapping.Columns.JANCode)

	// 標準単価。列が無い/空欄なら「非公表」としてnilのままにする。
	if priceText, ok := column(record, p.mapping.Columns.UnitPrice); ok && priceText != "" {
		price, err := parsePrice(priceText)
		if err != nil {
			row.ErrorMessage = fmt.Sprintf("単価を数値として読み取れません(%q)", priceText)
			return row
		}
		row.UnitPrice = &price
	}

	if discontinuedText, ok := column(record, p.mapping.Columns.Discontinued); ok {
		row.Discontinued = discontinuedText == p.discontinuedTrueValue()
	}

	// 医院別単価。1行が1医院分になっている形式のみ、この時点では1件だけ入れる
	// （同じ商品の行はParseの最後にまとめる）。
	if p.isFacilityPriceFormat() {
		facilityCode, _ := column(record, p.mapping.Columns.FacilityCode)
		priceText, _ := column(record, p.mapping.Columns.FacilityUnitPrice)
		if facilityCode == "" {
			row.ErrorMessage = "医院コードが取得できません"
			return row
		}
		price, err := parsePrice(priceText)
		if err != nil {
			row.ErrorMessage = fmt.Sprintf("医院別単価を数値として読み取れません(%q)", priceText)
			return row
		}
		row.FacilityPrices = []ingapp.CatalogFacilityPrice{{FacilityCode: facilityCode, UnitPrice: price}}
	}

	return row
}

func (p *MappingCsvParser) isFacilityPriceFormat() bool {
	return p.mapping.Columns.FacilityCode != nil && p.mapping.Columns.FacilityUnitPrice != nil
}

func (p *MappingCsvParser) discontinuedTrueValue() string {
	if p.mapping.DiscontinuedTrueValue != "" {
		return p.mapping.DiscontinuedTrueValue
	}
	return "1"
}

// groupByProductCode は医院別単価CSV（1商品×1医院で1行）を、商品1件＋医院別単価の配列にまとめる。
// 商品の属性は最初に現れた行のものを採用する。
func groupByProductCode(rows []ingapp.CatalogRow) []ingapp.CatalogRow {
	grouped := make([]ingapp.CatalogRow, 0, len(rows))
	indexByCode := make(map[string]int, len(rows))
	for _, row := range rows {
		if row.ErrorMessage != "" {
			grouped = append(grouped, row)
			continue
		}
		idx, ok := indexByCode[row.DistributorProductCode]
		if !ok {
			indexByCode[row.DistributorProductCode] = len(grouped)
			grouped = append(grouped, row)
			continue
		}
		grouped[idx].FacilityPrices = append(grouped[idx].FacilityPrices, row.FacilityPrices...)
	}
	return grouped
}

// column は指定された列番号の値を前後の空白を除いて返す。列指定が無い・行が短い場合は ok=false。
func column(record []string, index *int) (string, bool) {
	if index == nil || *index < 0 || *index >= len(record) {
		return "", false
	}
	return strings.TrimSpace(record[*index]), true
}

// parsePrice は "1,200" や "1200円" のような表記を数値に変換する。
func parsePrice(text string) (int, error) {
	cleaned := strings.NewReplacer(",", "", "，", "", "円", "", " ", "", "　", "").Replace(strings.TrimSpace(text))
	if cleaned == "" {
		return 0, fmt.Errorf("単価が空です")
	}
	return strconv.Atoi(cleaned)
}

func isEmptyRecord(record []string) bool {
	for _, field := range record {
		if strings.TrimSpace(field) != "" {
			return false
		}
	}
	return true
}

// decode は文字コードをUTF-8に揃える。国内の卸CSVはShift_JIS(CP932)が多い。
func decode(body []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "utf8", "utf-8":
		return bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF}), nil // BOMを除去
	case "shift_jis", "sjis", "cp932", "windows-31j":
		decoded, _, err := transform.Bytes(japanese.ShiftJIS.NewDecoder(), body)
		if err != nil {
			return nil, fmt.Errorf("Shift_JISとして読み取れませんでした: %w", err)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("未対応の文字コードです: %s", encoding)
	}
}
