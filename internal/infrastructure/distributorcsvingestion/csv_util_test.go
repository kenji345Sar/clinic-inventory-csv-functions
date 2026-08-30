package distributorcsvingestion_test

import (
	"bytes"
	"strings"
	"testing"

	inginfra "clinic-inventory-csv-functions/internal/infrastructure/distributorcsvingestion"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// 卸が申告した文字コードと、実際に届いたCSVの中身が食い違っている場合のテスト。
// 変換に失敗した文字は置換文字になるだけでエラーにならないため、
// 素通しすると文字化けした商品名がそのままDBに入る。ファイル単位で止まることを確認する。

func TestParseOroshiACatalogCSV_UTF8ヲSJISト申告シタ場合ハエラー(t *testing.T) {
	// 卸AはShift_JIS指定。そこへUTF-8のCSVが届いたケース。
	utf8CSV := "code,name,maker,jan,price,discontinued\n" +
		"A-0001,抗生剤 100mg,サンプル製薬,4900000000001,1200,0\n"

	_, err := inginfra.ParseOroshiACatalogCSV([]byte(utf8CSV))
	if err == nil {
		t.Fatal("エラーになるはず（文字化けしたまま取り込まれてしまう）")
	}
	if !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("実際の文字コードの見当を示すはず: %v", err)
	}
}

func TestParseOroshiBCatalogCSV_SJISヲUTF8ト申告シタ場合ハエラー(t *testing.T) {
	// 卸BはUTF-8指定。そこへShift_JISのCSVが届いたケース。
	utf8CSV := "卸商品コード,商品名,医院コード,単価\n" +
		"B-0001,消炎鎮痛剤,11111111-1111-1111-1111-111111111111,980\n"
	sjis, _, err := transform.Bytes(japanese.ShiftJIS.NewEncoder(), []byte(utf8CSV))
	if err != nil {
		t.Fatalf("テスト用データの用意に失敗: %v", err)
	}

	_, err = inginfra.ParseOroshiBCatalogCSV(sjis)
	if err == nil {
		t.Fatal("エラーになるはず（文字化けしたまま取り込まれてしまう）")
	}
	// ヘッダは日本語なので1行目で検出される。何行目かが出ることを確認する。
	if !strings.Contains(err.Error(), "行目") {
		t.Errorf("何行目かを示すはず: %v", err)
	}
}

func TestParseOroshiBCatalogCSV_BOM付きUTF8ハ読める(t *testing.T) {
	// ExcelがCSVとして保存すると先頭にBOMが付く。除去し損ねると
	// 1列目の卸商品コードの先頭に見えない文字が混ざり、突合が静かに失敗する。
	utf8CSV := "卸商品コード,商品名,医院コード,単価\n" +
		"B-0001,消炎鎮痛剤,11111111-1111-1111-1111-111111111111,980\n"
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte(utf8CSV)...)

	rows, err := inginfra.ParseOroshiBCatalogCSV(withBOM)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].DistributorProductCode != "B-0001" {
		t.Errorf("DistributorProductCode = %q, want \"B-0001\"（BOMが残っている）", rows[0].DistributorProductCode)
	}
	if bytes.Contains([]byte(rows[0].DistributorProductCode), []byte{0xEF, 0xBB, 0xBF}) {
		t.Error("卸商品コードにBOMが混ざっている")
	}
}
