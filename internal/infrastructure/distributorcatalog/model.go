package distributorcatalog

import "github.com/google/uuid"

// このファイルは反映先テーブルの列マッピング（gorm）。
//
// **テーブルの定義（作成・列変更）はこのリポジトリでは行わない。**
// スキーマの所有者はbackend（clinic-inventory）で、テーブルはbackend側のAutoMigrateで作られる。
// ここはあくまで「既にあるテーブルへ読み書きするための対応表」であり、AutoMigrateは呼ばない。
// backend側の定義を変更した場合は、こちらの構造体も合わせて更新する。

// DistributorProductModel は卸商品（distributor_products）。
// (distributor_id, distributor_product_code) のユニーク制約が、商品マスタCSV取り込みの突合キー。
type DistributorProductModel struct {
	ID                     uuid.UUID `gorm:"type:uuid;primaryKey"`
	DistributorID          uuid.UUID `gorm:"type:uuid;not null"`
	DistributorProductCode string    `gorm:"not null"`
	Name                   string    `gorm:"not null"`
	VendorName             string    `gorm:"not null"`
	VendorProductCode      string
	JANCode                string `gorm:"column:jan_code"`
	// 標準単価（税抜・円）。NULLは「卸が単価を公表していない」を表す（0円と区別する）。
	UnitPrice    *int
	Discontinued bool `gorm:"not null;default:false"`
}

func (DistributorProductModel) TableName() string { return "distributor_products" }

// DistributorProductFacilityPriceModel は医院別単価（distributor_product_facility_prices）。
// (卸商品, クリニック)が複合主キーで、取り込み時のupsertはこのキーのON CONFLICTで解決する。
type DistributorProductFacilityPriceModel struct {
	DistributorProductID uuid.UUID `gorm:"type:uuid;primaryKey"`
	FacilityID           uuid.UUID `gorm:"type:uuid;primaryKey"`
	UnitPrice            int       `gorm:"not null"`
}

func (DistributorProductFacilityPriceModel) TableName() string {
	return "distributor_product_facility_prices"
}
