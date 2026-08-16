package distributorcsvingestion_test

import (
	"testing"

	inginfra "clinic-inventory-csv-functions/internal/infrastructure/distributorcsvingestion"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// 卸ごとにパーサが分かれているので、テストも卸ごとに書く。
// 「この卸のCSVがどう読まれるか」が1つのテストで完結する。

func TestParseSamplePharmaCatalogCSV(t *testing.T) {
	csv := "コード,商品名,メーカー,JAN,単価,廃盤\n" +
		"S-0001,抗生剤 100mg,サンプル製薬,4900000000001,\"1,200\",0\n" +
		"S-0002,消炎鎮痛剤 50mg,サンプル製薬,,980,1\n" +
		"S-0003,単価未提供の商品,サンプル製薬,,,0\n"

	rows, err := inginfra.ParseSamplePharmaCatalogCSV([]byte(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3（1商品で1行なのでまとめない）", len(rows))
	}

	// 桁区切りのカンマ付き単価も数値として読める
	if rows[0].UnitPrice == nil || *rows[0].UnitPrice != 1200 {
		t.Errorf("rows[0].UnitPrice = %v, want 1200", rows[0].UnitPrice)
	}
	if rows[0].JANCode != "4900000000001" {
		t.Errorf("rows[0].JANCode = %q", rows[0].JANCode)
	}
	if rows[0].Discontinued {
		t.Error("rows[0] は販売中のはず")
	}
	if !rows[1].Discontinued {
		t.Error("rows[1] は廃盤のはず")
	}
	// 単価列が空欄なら「単価が無い」。0とは区別する
	if rows[2].UnitPrice != nil {
		t.Errorf("rows[2].UnitPrice = %v, want nil", *rows[2].UnitPrice)
	}

	t.Run("不正な行は理由を付けて返し、他の行は読み続ける", func(t *testing.T) {
		csv := "コード,商品名,メーカー,JAN,単価,廃盤\n" +
			",商品コードなし,サンプル製薬,,100,0\n" +
			"S-0002,単価が数値でない,サンプル製薬,,いくらでも,0\n" +
			"S-0003,正常な行,サンプル製薬,,500,0\n"

		rows, err := inginfra.ParseSamplePharmaCatalogCSV([]byte(csv))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("len(rows) = %d, want 3", len(rows))
		}
		if rows[0].ErrorMessage == "" || rows[1].ErrorMessage == "" {
			t.Error("不正な行にはエラー理由が入るべき")
		}
		if rows[2].ErrorMessage != "" {
			t.Errorf("正常な行にエラーが付いている: %s", rows[2].ErrorMessage)
		}
		// 行番号はCSV上の行（ヘッダを1行目とする）
		if rows[2].RowNo != 4 {
			t.Errorf("rows[2].RowNo = %d, want 4", rows[2].RowNo)
		}
	})
}

func TestParseOroshiACatalogCSV(t *testing.T) {
	// 卸AのCSVはShift_JISで届く
	utf8CSV := "コード,商品名,メーカー,単価\nA-0001,抗生剤 100mg,メーカーX,1200\n"
	sjis, _, err := transform.Bytes(japanese.ShiftJIS.NewEncoder(), []byte(utf8CSV))
	if err != nil {
		t.Fatalf("failed to encode test data: %v", err)
	}

	rows, err := inginfra.ParseOroshiACatalogCSV(sjis)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1", len(rows))
	}
	if rows[0].Name != "抗生剤 100mg" {
		t.Errorf("Name = %q（Shift_JISが復号できていない）", rows[0].Name)
	}
	if rows[0].VendorName != "メーカーX" {
		t.Errorf("VendorName = %q", rows[0].VendorName)
	}
	if rows[0].UnitPrice == nil || *rows[0].UnitPrice != 1200 {
		t.Errorf("UnitPrice = %v, want 1200", rows[0].UnitPrice)
	}
}

func TestParseOroshiBCatalogCSV(t *testing.T) {
	// 1商品×1医院で1行。B-1001 は2院、B-1002 は1院ぶんしか無い
	csv := "コード,商品名,医院コード,単価\n" +
		"B-1001,犬猫用抗生剤,CLINIC-A,2480\n" +
		"B-1002,犬猫用消炎鎮痛剤,CLINIC-A,1980\n" +
		"B-1001,犬猫用抗生剤,CLINIC-B,2560\n"

	rows, err := inginfra.ParseOroshiBCatalogCSV([]byte(csv))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2（商品単位にまとまる）", len(rows))
	}

	if len(rows[0].FacilityPrices) != 2 {
		t.Fatalf("B-1001 の医院別単価 = %d件, want 2", len(rows[0].FacilityPrices))
	}
	if rows[0].FacilityPrices[1].FacilityCode != "CLINIC-B" || rows[0].FacilityPrices[1].UnitPrice != 2560 {
		t.Errorf("FacilityPrices[1] = %+v", rows[0].FacilityPrices[1])
	}
	// 全医院共通の定価は存在しない
	if rows[0].UnitPrice != nil {
		t.Errorf("UnitPrice = %v, want nil", *rows[0].UnitPrice)
	}
	// メーカー列が無いので既定値で補う（卸商品の必須項目のため）
	if rows[0].VendorName == "" {
		t.Error("VendorName が空（既定値で補われるはず）")
	}
	if len(rows[1].FacilityPrices) != 1 {
		t.Errorf("B-1002 の医院別単価 = %d件, want 1（1院でしか扱っていない）", len(rows[1].FacilityPrices))
	}

	t.Run("医院コードが空の行はエラーになる", func(t *testing.T) {
		csv := "コード,商品名,医院コード,単価\nB-1001,犬猫用抗生剤,,2480\n"
		rows, err := inginfra.ParseOroshiBCatalogCSV([]byte(csv))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rows[0].ErrorMessage == "" {
			t.Error("医院コードが空ならエラー理由が入るべき")
		}
	})
}

func TestParserRegistry(t *testing.T) {
	registry := inginfra.DefaultParsers()

	if _, err := registry.Resolve("oroshi-b"); err != nil {
		t.Errorf("登録済みの卸コードは解決できるべき: %v", err)
	}
	// 未対応の卸のフォルダにCSVが置かれても取り込まない
	if _, err := registry.Resolve("unknown-distributor"); err == nil {
		t.Error("未登録の卸コードはエラーになるべき")
	}
}
