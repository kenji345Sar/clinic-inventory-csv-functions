package distributorcatalog

import (
	"context"

	"clinic-inventory-csv-functions/internal/infrastructure/database"

	"gorm.io/gorm"
)

// dbOf は使用するDBハンドルを返す。商品マスタCSVの反映では「卸商品」と「医院別単価」を
// 1つのトランザクションでまとめて更新するため、contextにトランザクションが載っていれば
// そちらを使う（載っていなければ通常の接続）。
func (r *DistributorProductRepository) dbOf(ctx context.Context) *gorm.DB {
	return database.From(ctx, r.db).WithContext(ctx)
}

func (r *FacilityPriceRepository) dbOf(ctx context.Context) *gorm.DB {
	return database.From(ctx, r.db).WithContext(ctx)
}
