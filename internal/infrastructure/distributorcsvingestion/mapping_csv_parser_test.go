package distributorcsvingestion_test

import (
	"testing"

	inginfra "clinic-inventory-csv-functions/internal/infrastructure/distributorcsvingestion"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

func col(i int) *int { return &i }

// 商品ごとに単価を持つ、もっとも素直な形式の卸。
func standardMapping() inginfra.ColumnMapping {
	return inginfra.ColumnMapping{
		HasHeader: true,
		Columns: inginfra.ColumnIndexes{
			DistributorProductCode: col(0),
			Name:                   col(1),
			VendorName:             col(2),
			JANCode:                col(3),
			UnitPrice:              col(4),
			Discontinued:           col(5),
		},
	}
}

func TestMappingCsvParser(t *testing.T) {
	t.Run("商品ごとの単価を持つCSVを読み取れる", func(t *testing.T) {
		csv := "コード,商品名,メーカー,JAN,単価,廃盤\n" +
			"D-0001,抗生剤 100mg,サンプル製薬,4900000000001,\"1,200\",0\n" +
			"D-0002,消炎鎮痛剤 50mg,サンプル製薬,,980,1\n"

		rows, err := inginfra.NewMappingCsvParser(standardMapping()).Parse([]byte(csv))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("len(rows) = %d, want 2", len(rows))
		}
		if rows[0].DistributorProductCode != "D-0001" || rows[0].Name != "抗生剤 100mg" {
			t.Errorf("row0 = %+v", rows[0])
		}
		// 桁区切りのカンマ付き単価も数値として読める
		if rows[0].UnitPrice == nil || *rows[0].UnitPrice != 1200 {
			t.Errorf("row0.UnitPrice = %v, want 1200", rows[0].UnitPrice)
		}
		if rows[0].Discontinued {
			t.Error("row0.Discontinued should be false")
		}
		if !rows[1].Discontinued {
			t.Error("row1.Discontinued should be true")
		}
	})

	t.Run("単価列が空欄なら非公表として扱う", func(t *testing.T) {
		csv := "コード,商品名,メーカー,JAN,単価,廃盤\nD-0001,抗生剤,サンプル製薬,,,0\n"

		rows, err := inginfra.NewMappingCsvParser(standardMapping()).Parse([]byte(csv))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rows[0].ErrorMessage != "" {
			t.Fatalf("unexpected error message: %s", rows[0].ErrorMessage)
		}
		if rows[0].UnitPrice != nil {
			t.Errorf("UnitPrice = %v, want nil(非公表)", *rows[0].UnitPrice)
		}
	})

	t.Run("単価列がそもそも無い卸（非公表）も読み取れる", func(t *testing.T) {
		mapping := inginfra.ColumnMapping{
			HasHeader:         false,
			DefaultVendorName: "非公表卸メーカー",
			Columns: inginfra.ColumnIndexes{
				DistributorProductCode: col(0),
				Name:                   col(1),
			},
		}
		rows, err := inginfra.NewMappingCsvParser(mapping).Parse([]byte("D-0001,抗生剤\n"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rows[0].UnitPrice != nil {
			t.Errorf("UnitPrice = %v, want nil", *rows[0].UnitPrice)
		}
		// ベンダー名の列が無い卸は設定の既定値で補う（ベンダー名は卸商品の必須項目のため）
		if rows[0].VendorName != "非公表卸メーカー" {
			t.Errorf("VendorName = %q, want %q", rows[0].VendorName, "非公表卸メーカー")
		}
	})

	t.Run("不正な行は理由を付けて返し、他の行の読み取りは続ける", func(t *testing.T) {
		csv := "コード,商品名,メーカー,JAN,単価,廃盤\n" +
			",商品名なしコード,サンプル製薬,,100,0\n" +
			"D-0002,単価が数値でない,サンプル製薬,,いくらでも,0\n" +
			"D-0003,正常な行,サンプル製薬,,500,0\n"

		rows, err := inginfra.NewMappingCsvParser(standardMapping()).Parse([]byte(csv))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("len(rows) = %d, want 3", len(rows))
		}
		if rows[0].ErrorMessage == "" {
			t.Error("卸商品コードが空の行はエラーになるべき")
		}
		if rows[1].ErrorMessage == "" {
			t.Error("単価が数値でない行はエラーになるべき")
		}
		if rows[2].ErrorMessage != "" {
			t.Errorf("正常な行にエラーが付いている: %s", rows[2].ErrorMessage)
		}
		// 行番号はCSV上の行（ヘッダを1行目とする）
		if rows[2].RowNo != 4 {
			t.Errorf("RowNo = %d, want 4", rows[2].RowNo)
		}
	})

	t.Run("医院別単価CSVは同じ商品の行をまとめる", func(t *testing.T) {
		mapping := inginfra.ColumnMapping{
			HasHeader:         true,
			DefaultVendorName: "サンプル製薬",
			Columns: inginfra.ColumnIndexes{
				DistributorProductCode: col(0),
				Name:                   col(1),
				FacilityCode:           col(2),
				FacilityUnitPrice:      col(3),
			},
		}
		csv := "コード,商品名,医院コード,単価\n" +
			"D-0001,抗生剤,CLINIC-A,1100\n" +
			"D-0001,抗生剤,CLINIC-B,1250\n" +
			"D-0002,鎮痛剤,CLINIC-A,900\n"

		rows, err := inginfra.NewMappingCsvParser(mapping).Parse([]byte(csv))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("len(rows) = %d, want 2（商品単位にまとまる）", len(rows))
		}
		if len(rows[0].FacilityPrices) != 2 {
			t.Fatalf("len(FacilityPrices) = %d, want 2", len(rows[0].FacilityPrices))
		}
		if rows[0].FacilityPrices[1].FacilityCode != "CLINIC-B" || rows[0].FacilityPrices[1].UnitPrice != 1250 {
			t.Errorf("FacilityPrices[1] = %+v", rows[0].FacilityPrices[1])
		}
		// 医院別単価の卸は標準単価を持たない
		if rows[0].UnitPrice != nil {
			t.Errorf("UnitPrice = %v, want nil", *rows[0].UnitPrice)
		}
	})

	t.Run("Shift_JISのCSVを読み取れる", func(t *testing.T) {
		utf8CSV := "コード,商品名,メーカー,JAN,単価,廃盤\nD-0001,抗生剤 100mg,サンプル製薬,,1200,0\n"
		sjis, _, err := transform.Bytes(japanese.ShiftJIS.NewEncoder(), []byte(utf8CSV))
		if err != nil {
			t.Fatalf("failed to encode test data: %v", err)
		}

		mapping := standardMapping()
		mapping.Encoding = "shift_jis"
		rows, err := inginfra.NewMappingCsvParser(mapping).Parse(sjis)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rows[0].Name != "抗生剤 100mg" {
			t.Errorf("Name = %q, want %q", rows[0].Name, "抗生剤 100mg")
		}
	})
}
