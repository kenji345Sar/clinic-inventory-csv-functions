package distributorcatalog

import (
	"context"

	distdomain "clinic-inventory-csv-functions/internal/domain/distributorcatalog"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type DistributorProductRepository struct {
	db *gorm.DB
}

func NewDistributorProductRepository(db *gorm.DB) *DistributorProductRepository {
	return &DistributorProductRepository{db: db}
}

// FindByDistributorAndCode は突合キーで1件引く。見つからない場合は (nil, nil)。
// 取り込みでは「無い＝新規行」が毎行の正常系のため、Firstではなく Limit(1).Find を使い
// 「見つからない」をエラー扱いにしない（ログにも出さない）。
func (r *DistributorProductRepository) FindByDistributorAndCode(ctx context.Context, distributorID shareddomain.ID, code string) (*distdomain.DistributorProduct, error) {
	var models []DistributorProductModel
	err := r.dbOf(ctx).
		Where("distributor_id = ? AND distributor_product_code = ?", uuid.UUID(distributorID), code).
		Limit(1).
		Find(&models).Error
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, nil
	}
	return toDomainDistributorProduct(models[0]), nil
}

func (r *DistributorProductRepository) Create(ctx context.Context, product *distdomain.DistributorProduct) error {
	model := toDistributorProductModel(product)
	return r.dbOf(ctx).Create(&model).Error
}

// Update は既存の卸商品を上書き保存する。Saveではなく列を指定したUpdatesにしているのは、
// unit_priceにNULL（単価非公表）を書き戻せるようにするため。
func (r *DistributorProductRepository) Update(ctx context.Context, product *distdomain.DistributorProduct) error {
	model := toDistributorProductModel(product)
	return r.dbOf(ctx).
		Model(&DistributorProductModel{}).
		Where("id = ?", model.ID).
		Updates(map[string]any{
			"name":                model.Name,
			"vendor_name":         model.VendorName,
			"vendor_product_code": model.VendorProductCode,
			"jan_code":            model.JANCode,
			"unit_price":          model.UnitPrice,
			"discontinued":        model.Discontinued,
		}).Error
}

func toDistributorProductModel(p *distdomain.DistributorProduct) DistributorProductModel {
	return DistributorProductModel{
		ID:                     uuid.UUID(p.ID()),
		DistributorID:          uuid.UUID(p.DistributorID()),
		DistributorProductCode: p.DistributorProductCode(),
		Name:                   p.Name(),
		VendorName:             p.VendorName(),
		VendorProductCode:      p.VendorProductCode(),
		JANCode:                p.JANCode(),
		UnitPrice:              p.UnitPrice(),
		Discontinued:           p.Discontinued(),
	}
}

func toDomainDistributorProduct(model DistributorProductModel) *distdomain.DistributorProduct {
	return distdomain.ReconstructDistributorProduct(
		shareddomain.ID(model.ID),
		shareddomain.ID(model.DistributorID),
		model.DistributorProductCode,
		model.Name,
		model.VendorName,
		model.VendorProductCode,
		model.JANCode,
		model.UnitPrice,
		model.Discontinued,
	)
}
