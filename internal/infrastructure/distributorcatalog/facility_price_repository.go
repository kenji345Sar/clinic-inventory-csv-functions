package distributorcatalog

import (
	"context"

	distdomain "clinic-inventory-csv-functions/internal/domain/distributorcatalog"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FacilityPriceRepository struct {
	db *gorm.DB
}

func NewFacilityPriceRepository(db *gorm.DB) *FacilityPriceRepository {
	return &FacilityPriceRepository{db: db}
}

// FindByDistributor は医院別単価テーブルにはdistributor_idが無いため、
// その卸の商品IDをサブクエリで引いて絞る（1文で済む）。
func (r *FacilityPriceRepository) FindByDistributor(ctx context.Context, distributorID shareddomain.ID) ([]*distdomain.FacilityPrice, error) {
	productIDs := r.dbOf(ctx).
		Model(&DistributorProductModel{}).
		Select("id").
		Where("distributor_id = ?", uuid.UUID(distributorID))
	var models []DistributorProductFacilityPriceModel
	if err := r.dbOf(ctx).Where("distributor_product_id IN (?)", productIDs).Find(&models).Error; err != nil {
		return nil, err
	}
	prices := make([]*distdomain.FacilityPrice, 0, len(models))
	for _, m := range models {
		prices = append(prices, distdomain.ReconstructFacilityPrice(
			shareddomain.ID(m.DistributorProductID), shareddomain.ID(m.FacilityID), m.UnitPrice,
		))
	}
	return prices, nil
}

// UpsertAll は(卸商品, クリニック)が既にあれば単価を更新し、無ければ挿入する。
// 複合主キーの衝突をDB側のON CONFLICTで解決する。
func (r *FacilityPriceRepository) UpsertAll(ctx context.Context, prices []*distdomain.FacilityPrice) error {
	if len(prices) == 0 {
		return nil
	}
	models := make([]DistributorProductFacilityPriceModel, 0, len(prices))
	for _, p := range prices {
		models = append(models, DistributorProductFacilityPriceModel{
			DistributorProductID: uuid.UUID(p.DistributorProductID()),
			FacilityID:           uuid.UUID(p.FacilityID()),
			UnitPrice:            p.UnitPrice(),
		})
	}
	return r.dbOf(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "distributor_product_id"}, {Name: "facility_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"unit_price"}),
		}).
		CreateInBatches(&models, upsertBatchSize).Error
}
