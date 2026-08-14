package distributorcatalog

import (
	"context"

	distdomain "clinic-inventory-csv-functions/internal/domain/distributorcatalog"

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

// UpsertAll は(卸商品, クリニック)が既にあれば単価を更新し、無ければ挿入する。
// 複合主キーの衝突をDB側のON CONFLICTで解決するため、件数分のSELECTが不要になる。
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
		Create(&models).Error
}
