package distributorcatalog_test

import (
	"testing"

	distdomain "clinic-inventory-csv-functions/internal/domain/distributorcatalog"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

func TestNewFacilityPrice(t *testing.T) {
	productID := shareddomain.NewID()
	facilityID := shareddomain.NewID()

	tests := []struct {
		name       string
		productID  shareddomain.ID
		facilityID shareddomain.ID
		unitPrice  int
		wantErr    string
	}{
		{name: "卸商品・クリニック・1円以上の単価があれば作れる", productID: productID, facilityID: facilityID, unitPrice: 2480},
		{name: "単価は1円なら作れる", productID: productID, facilityID: facilityID, unitPrice: 1},
		{name: "卸商品が未指定なら作れない", productID: shareddomain.ID{}, facilityID: facilityID, unitPrice: 2480, wantErr: "卸商品の指定は必須です"},
		{name: "クリニックが未指定なら作れない", productID: productID, facilityID: shareddomain.ID{}, unitPrice: 2480, wantErr: "クリニックの指定は必須です"},
		{name: "単価が0円なら作れない", productID: productID, facilityID: facilityID, unitPrice: 0, wantErr: "医院別単価は1円以上で指定してください"},
		{name: "単価がマイナスなら作れない", productID: productID, facilityID: facilityID, unitPrice: -1, wantErr: "医院別単価は1円以上で指定してください"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			price, err := distdomain.NewFacilityPrice(tt.productID, tt.facilityID, tt.unitPrice)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if price != nil {
					t.Error("エラーのときは医院別単価を返さないべき")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if price.DistributorProductID() != tt.productID || price.FacilityID() != tt.facilityID || price.UnitPrice() != tt.unitPrice {
				t.Errorf("値が引数どおりでない: %+v", price)
			}
		})
	}
}

func TestReconstructFacilityPrice_業務ルールを確かめない(t *testing.T) {
	price := distdomain.ReconstructFacilityPrice(shareddomain.NewID(), shareddomain.NewID(), 0)
	if price == nil || price.UnitPrice() != 0 {
		t.Fatal("ルールに合わない値でも復元できるべき")
	}
}
