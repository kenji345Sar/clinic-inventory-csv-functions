package distributorcsvingestion

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

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
//
// 変換できないバイトがあっても、x/textのデコーダはエラーを返さず置換文字(U+FFFD)に
// 差し替えて先へ進める。そのまま通すと「取り込みは成功したが商品名が文字化けしている」
// 行が誰にも気づかれずDBに入るため、化けを検出した時点でファイルごと止める。
// 原因の大半は、卸が申告した文字コードと実際に届いたCSVの中身の食い違い。
func decode(body []byte, encoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", encodingUTF8, "utf-8":
		decoded := bytes.TrimPrefix(body, []byte{0xEF, 0xBB, 0xBF}) // BOMを除去
		if i := invalidUTF8Index(decoded); i >= 0 {
			return nil, fmt.Errorf("%d行目にUTF-8として読めないバイトがあります（CSVが実際にはShift_JISの可能性があります）", lineNo(decoded, i))
		}
		return decoded, nil
	case encodingShiftJIS, "sjis", "cp932", "windows-31j":
		// UTF-8のCSVをShift_JISとして読むと、多くは置換文字にならず「蝠�刀蜷�」のような
		// 妥当な漢字に化けるため、下の置換文字チェックだけでは素通りする。
		// 日本語のShift_JISがファイル全体で妥当なUTF-8にもなることは実質ないので、
		// UTF-8として読めてしまう時点で申告と中身が食い違っていると判断する。
		if hasNonASCII(body) && utf8.Valid(body) {
			return nil, fmt.Errorf("Shift_JISと指定されていますが中身はUTF-8として読めます（卸から届いたCSVの文字コードが申告と違う可能性があります）")
		}
		decoded, _, err := transform.Bytes(japanese.ShiftJIS.NewDecoder(), body)
		if err != nil {
			return nil, fmt.Errorf("Shift_JISとして読み取れませんでした: %w", err)
		}
		if i := bytes.IndexRune(decoded, utf8.RuneError); i >= 0 {
			return nil, fmt.Errorf("%d行目にShift_JISとして解釈できない文字があります（CSVが実際にはUTF-8の可能性があります）", lineNo(decoded, i))
		}
		return decoded, nil
	default:
		return nil, fmt.Errorf("未対応の文字コードです: %s", encoding)
	}
}

// invalidUTF8Index はUTF-8として不正な最初のバイト位置を返す。すべて正しければ-1。
func invalidUTF8Index(b []byte) int {
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			return i
		}
		i += size
	}
	return -1
}

// hasNonASCII は半角英数字以外のバイトを含むかを返す。
// 全部ASCIIなら、どの文字コードで読んでも結果が同じなので判定の対象外にする。
func hasNonASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return true
		}
	}
	return false
}

// lineNo はバイト位置が何行目か（1始まり）を返す。どこを直せばよいかを人に示すため。
func lineNo(b []byte, index int) int {
	return bytes.Count(b[:index], []byte("\n")) + 1
}
