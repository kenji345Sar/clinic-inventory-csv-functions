package distributorcatalog_test

import (
	"testing"

	distdomain "clinic-inventory-csv-functions/internal/domain/distributorcatalog"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// DistributorProduct の業務ルールを、ルール1つにつきテスト1行で確かめる。
// 表の name がそのままルールの一覧として読めるようにしている。

func intPtr(v int) *int { return &v }

func TestNewDistributorProduct(t *testing.T) {
	distributorID := shareddomain.NewID()

	tests := []struct {
		name          string
		distributorID shareddomain.ID
		code          string
		productName   string
		vendorName    string
		unitPrice     *int
		wantErr       string
	}{
		{name: "必須項目と1円以上の単価があれば作れる", distributorID: distributorID, code: "B-1001", productName: "犬猫用抗生剤", vendorName: "卸B取扱", unitPrice: intPtr(2480)},
		{name: "単価は未設定でも作れる", distributorID: distributorID, code: "B-1001", productName: "犬猫用抗生剤", vendorName: "卸B取扱", unitPrice: nil},
		{name: "単価は1円なら作れる", distributorID: distributorID, code: "B-1001", productName: "犬猫用抗生剤", vendorName: "卸B取扱", unitPrice: intPtr(1)},
		{name: "卸業者が未指定なら作れない", distributorID: shareddomain.ID{}, code: "B-1001", productName: "犬猫用抗生剤", vendorName: "卸B取扱", wantErr: "卸業者の指定は必須です"},
		{name: "卸商品コードが空なら作れない", distributorID: distributorID, code: "", productName: "犬猫用抗生剤", vendorName: "卸B取扱", wantErr: "卸商品コードは必須です"},
		{name: "商品名が空なら作れない", distributorID: distributorID, code: "B-1001", productName: "", vendorName: "卸B取扱", wantErr: "商品名は必須です"},
		{name: "ベンダー名が空なら作れない", distributorID: distributorID, code: "B-1001", productName: "犬猫用抗生剤", vendorName: "", wantErr: "ベンダー名は必須です"},
		{name: "単価が0円なら作れない", distributorID: distributorID, code: "B-1001", productName: "犬猫用抗生剤", vendorName: "卸B取扱", unitPrice: intPtr(0), wantErr: "単価は1円以上で指定してください"},
		{name: "単価がマイナスなら作れない", distributorID: distributorID, code: "B-1001", productName: "犬猫用抗生剤", vendorName: "卸B取扱", unitPrice: intPtr(-100), wantErr: "単価は1円以上で指定してください"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			product, err := distdomain.NewDistributorProduct(tt.distributorID, tt.code, tt.productName, tt.vendorName, tt.unitPrice)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if product != nil {
					t.Error("エラーのときは卸商品を返さないべき")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if product.DistributorProductCode() != tt.code || product.Name() != tt.productName {
				t.Errorf("値が引数どおりでない: code=%q name=%q", product.DistributorProductCode(), product.Name())
			}
		})
	}

	t.Run("作成時は廃盤ではなく、システム内部IDが発行され、ベンダー商品コードとJANは未設定", func(t *testing.T) {
		product, err := distdomain.NewDistributorProduct(distributorID, "B-1001", "犬猫用抗生剤", "卸B取扱", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if product.Discontinued() {
			t.Error("作成時は廃盤ではないべき")
		}
		if product.ID().IsZero() {
			t.Error("システム内部IDが発行されていない")
		}
		if product.VendorProductCode() != "" || product.JANCode() != "" {
			t.Errorf("ベンダー商品コード・JANは未設定のはず: %q / %q", product.VendorProductCode(), product.JANCode())
		}
	})

	t.Run("作成のたびに別のシステム内部IDが発行される", func(t *testing.T) {
		a, _ := distdomain.NewDistributorProduct(distributorID, "B-1001", "犬猫用抗生剤", "卸B取扱", nil)
		b, _ := distdomain.NewDistributorProduct(distributorID, "B-1001", "犬猫用抗生剤", "卸B取扱", nil)
		if a.ID() == b.ID() {
			t.Error("同じIDが発行された")
		}
	})
}

func TestDistributorProduct_HasUnitPrice(t *testing.T) {
	distributorID := shareddomain.NewID()

	undisclosed, _ := distdomain.NewDistributorProduct(distributorID, "B-1001", "犬猫用抗生剤", "卸B取扱", nil)
	if undisclosed.HasUnitPrice() {
		t.Error("単価が未設定なら false のはず")
	}
	priced, _ := distdomain.NewDistributorProduct(distributorID, "A-0001", "抗生剤", "メーカーX", intPtr(1200))
	if !priced.HasUnitPrice() {
		t.Error("単価があれば true のはず")
	}
}

func TestDistributorProduct_Discontinue(t *testing.T) {
	product, _ := distdomain.NewDistributorProduct(shareddomain.NewID(), "S-0001", "抗生剤", "サンプル製薬", nil)
	product.Discontinue()
	if !product.Discontinued() {
		t.Error("Discontinue の後は廃盤のはず")
	}
	// 廃盤にしても商品情報は残る（削除ではない）
	if product.Name() != "抗生剤" || product.DistributorProductCode() != "S-0001" {
		t.Errorf("廃盤で商品情報が変わった: code=%q name=%q", product.DistributorProductCode(), product.Name())
	}
}

// catalogValues は UpdateFromDistributorCatalog に渡すCSVの値。
type catalogValues struct {
	name, vendorName, vendorProductCode, janCode string
	unitPrice                                    *int
	discontinued                                 bool
}

// 比較の基準になる既存の卸商品（DBから復元した想定）。
func existingProduct(v catalogValues) *distdomain.DistributorProduct {
	return distdomain.ReconstructDistributorProduct(
		shareddomain.NewID(), shareddomain.NewID(), "S-0001",
		v.name, v.vendorName, v.vendorProductCode, v.janCode, v.unitPrice, v.discontinued,
	)
}

func TestDistributorProduct_UpdateFromDistributorCatalog(t *testing.T) {
	current := catalogValues{
		name: "抗生剤 100mg", vendorName: "サンプル製薬", vendorProductCode: "V-01",
		janCode: "4900000000001", unitPrice: intPtr(1200), discontinued: false,
	}
	with := func(change func(v *catalogValues)) catalogValues {
		v := current
		change(&v)
		return v
	}

	t.Run("変更の判定", func(t *testing.T) {
		tests := []struct {
			name        string
			csv         catalogValues
			wantChanged bool
		}{
			{name: "すべて同じなら変更なし", csv: current, wantChanged: false},
			{name: "単価が同じ金額の別ポインタでも変更なし", csv: with(func(v *catalogValues) { v.unitPrice = intPtr(1200) }), wantChanged: false},
			{name: "商品名が違えば変更あり", csv: with(func(v *catalogValues) { v.name = "抗生剤 100mg（改定）" }), wantChanged: true},
			{name: "ベンダー名が違えば変更あり", csv: with(func(v *catalogValues) { v.vendorName = "別製薬" }), wantChanged: true},
			{name: "ベンダー商品コードが違えば変更あり", csv: with(func(v *catalogValues) { v.vendorProductCode = "V-02" }), wantChanged: true},
			{name: "JANが違えば変更あり", csv: with(func(v *catalogValues) { v.janCode = "4900000000002" }), wantChanged: true},
			{name: "単価の金額が違えば変更あり", csv: with(func(v *catalogValues) { v.unitPrice = intPtr(1350) }), wantChanged: true},
			{name: "単価ありから未設定になれば変更あり", csv: with(func(v *catalogValues) { v.unitPrice = nil }), wantChanged: true},
			{name: "廃盤になれば変更あり", csv: with(func(v *catalogValues) { v.discontinued = true }), wantChanged: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				product := existingProduct(current)
				changed, err := product.UpdateFromDistributorCatalog(tt.csv.name, tt.csv.vendorName, tt.csv.vendorProductCode, tt.csv.janCode, tt.csv.unitPrice, tt.csv.discontinued)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if changed != tt.wantChanged {
					t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
				}
			})
		}
	})

	t.Run("単価が未設定同士なら変更なし、未設定から単価ありになれば変更あり", func(t *testing.T) {
		undisclosed := with(func(v *catalogValues) { v.unitPrice = nil })

		product := existingProduct(undisclosed)
		changed, _ := product.UpdateFromDistributorCatalog(undisclosed.name, undisclosed.vendorName, undisclosed.vendorProductCode, undisclosed.janCode, nil, undisclosed.discontinued)
		if changed {
			t.Error("未設定同士は変更なしのはず")
		}

		product = existingProduct(undisclosed)
		changed, _ = product.UpdateFromDistributorCatalog(undisclosed.name, undisclosed.vendorName, undisclosed.vendorProductCode, undisclosed.janCode, intPtr(1200), undisclosed.discontinued)
		if !changed {
			t.Error("未設定から単価ありは変更ありのはず")
		}
	})

	t.Run("CSVの値がすべて反映され、未設定の単価は前回の単価を消す", func(t *testing.T) {
		product := existingProduct(current)
		_, err := product.UpdateFromDistributorCatalog("抗生剤（改定）", "別製薬", "V-02", "", nil, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if product.Name() != "抗生剤（改定）" || product.VendorName() != "別製薬" || product.VendorProductCode() != "V-02" {
			t.Errorf("商品情報が反映されていない: %q / %q / %q", product.Name(), product.VendorName(), product.VendorProductCode())
		}
		if product.JANCode() != "" {
			t.Errorf("JANは空で上書きされるはず: %q", product.JANCode())
		}
		if product.HasUnitPrice() {
			t.Errorf("単価は未設定で上書きされるはず: %v", *product.UnitPrice())
		}
		if !product.Discontinued() {
			t.Error("廃盤が反映されていない")
		}
	})

	t.Run("廃盤はCSVで0に戻れば解除される", func(t *testing.T) {
		discontinued := with(func(v *catalogValues) { v.discontinued = true })
		product := existingProduct(discontinued)
		changed, _ := product.UpdateFromDistributorCatalog(current.name, current.vendorName, current.vendorProductCode, current.janCode, current.unitPrice, false)
		if !changed || product.Discontinued() {
			t.Errorf("廃盤が解除されていない: changed=%v discontinued=%v", changed, product.Discontinued())
		}
	})

	t.Run("卸業者・卸商品コード・システム内部IDは変わらない", func(t *testing.T) {
		product := existingProduct(current)
		id, distributorID, code := product.ID(), product.DistributorID(), product.DistributorProductCode()
		_, _ = product.UpdateFromDistributorCatalog("抗生剤（改定）", "別製薬", "", "", intPtr(999), true)
		if product.ID() != id || product.DistributorID() != distributorID || product.DistributorProductCode() != code {
			t.Error("突合キーまたはIDが変わった")
		}
	})

	t.Run("不正な値ならエラーになり、商品は書き換わらない", func(t *testing.T) {
		tests := []struct {
			name    string
			csv     catalogValues
			wantErr string
		}{
			{name: "商品名が空", csv: with(func(v *catalogValues) { v.name = "" }), wantErr: "商品名は必須です"},
			{name: "ベンダー名が空", csv: with(func(v *catalogValues) { v.vendorName = "" }), wantErr: "ベンダー名は必須です"},
			{name: "単価が0円", csv: with(func(v *catalogValues) { v.unitPrice = intPtr(0) }), wantErr: "単価は1円以上で指定してください"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				product := existingProduct(current)
				// 不正な値と一緒に、正しい値の変更（JAN・廃盤）も渡す。それも反映されないこと
				changed, err := product.UpdateFromDistributorCatalog(tt.csv.name, tt.csv.vendorName, tt.csv.vendorProductCode, "4900000000999", tt.csv.unitPrice, true)
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if changed {
					t.Error("エラーのときは changed = false のはず")
				}
				if product.Name() != current.name || product.JANCode() != current.janCode || product.Discontinued() {
					t.Errorf("エラーなのに書き換わった: name=%q jan=%q discontinued=%v", product.Name(), product.JANCode(), product.Discontinued())
				}
			})
		}
	})
}

func TestReconstructDistributorProduct_業務ルールを確かめない(t *testing.T) {
	// DBに既にある値は、ルールを厳しくした後でも読めなければならない
	product := distdomain.ReconstructDistributorProduct(shareddomain.NewID(), shareddomain.NewID(), "S-0001", "", "", "", "", intPtr(0), false)
	if product == nil {
		t.Fatal("ルールに合わない値でも復元できるべき")
	}
}
