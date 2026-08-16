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

// このファイルは卸別パーサ（catalog_*.go）が共通で使う道具。
// 「CSVをどう読むか」は卸ごとに違うが、文字コード変換・数値の読み方・
// 医院別単価のまとめ方といった部品は同じなので、ここに集めている。

// 文字コード。卸別パーサが readRecords に渡す。
const (
	encodingUTF8     = "utf8"
	encodingShiftJIS = "shift_jis"
)

// readRecords はCSVをUTF-8に直して行の配列にする。
// hasHeader=true なら1行目を読み飛ばし、空行も除く。
// 返す rowNo はCSV上の行番号（ヘッダを1行目とする）で、エラー報告に使う。
func readRecords(body []byte, encoding string, hasHeader bool) ([]csvRecord, error) {
	decoded, err := decode(body, encoding)
	if err != nil {
		return nil, err
	}

	reader := csv.NewReader(bytes.NewReader(decoded))
	// 卸のCSVは行によって列数が違うことがあるため、列数の一致チェックはしない。
	reader.FieldsPerRecord = -1

	records := make([]csvRecord, 0)
	rowNo := 0
	for {
		fields, err := reader.Read()
		if err == io.EOF {
			break
		}
		rowNo++
		if err != nil {
			return nil, fmt.Errorf("%d行目でCSVの読み取りに失敗しました: %w", rowNo, err)
		}
		if rowNo == 1 && hasHeader {
			continue
		}
		if isEmptyRecord(fields) {
			continue
		}
		records = append(records, csvRecord{RowNo: rowNo, Fields: fields})
	}
	return records, nil
}

// csvRecord はCSV1行分。行番号を持つのは、読み取れなかった行を人が特定できるようにするため。
type csvRecord struct {
	RowNo  int
	Fields []string
}

// Raw はステージングに残すCSV原文（表示用に再結合したもの）。
func (r csvRecord) Raw() string { return strings.Join(r.Fields, ",") }

// Column は指定した列番号の値を、前後の空白を除いて返す。行が短ければ空文字。
func (r csvRecord) Column(index int) string {
	if index < 0 || index >= len(r.Fields) {
		return ""
	}
	return strings.TrimSpace(r.Fields[index])
}

// invalidRow は読み取れなかった行を、理由付きの中間表現として返す。
// パースは行単位で継続する（1行の不備でファイル全体が見えなくなるのを避ける）。
func invalidRow(r csvRecord, reason string) ingapp.CatalogRow {
	return ingapp.CatalogRow{RowNo: r.RowNo, Raw: r.Raw(), ErrorMessage: reason}
}

// parsePrice は "1,200" や "1200円" のような表記を数値に変換する。
func parsePrice(text string) (int, error) {
	cleaned := strings.NewReplacer(",", "", "，", "", "円", "", " ", "", "　", "").Replace(strings.TrimSpace(text))
	if cleaned == "" {
		return 0, fmt.Errorf("単価が空です")
	}
	return strconv.Atoi(cleaned)
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

func isEmptyRecord(fields []string) bool {
	for _, field := range fields {
		if strings.TrimSpace(field) != "" {
			return false
		}
	}
	return true
}

// decode は文字コードをUTF-8に揃える。国内の卸CSVはShift_JIS(CP932)が多い。
func decode(body []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", encodingUTF8, "utf-8":
		return bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF}), nil // BOMを除去
	case encodingShiftJIS, "sjis", "cp932", "windows-31j":
		decoded, _, err := transform.Bytes(japanese.ShiftJIS.NewDecoder(), body)
		if err != nil {
			return nil, fmt.Errorf("Shift_JISとして読み取れませんでした: %w", err)
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("未対応の文字コードです: %s", encoding)
	}
}
