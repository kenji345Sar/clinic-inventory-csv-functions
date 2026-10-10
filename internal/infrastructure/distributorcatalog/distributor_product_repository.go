package distributorcatalog

import (
	"context"

	distdomain "clinic-inventory-csv-functions/internal/domain/distributorcatalog"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// upsertBatchSize は1文のINSERTに並べる行数。PostgreSQLは1文のパラメータ数が65,535までで、
// 卸商品は1行9列のため、これを超える行数を1文に並べると失敗する。
// 超える分は同じトランザクションの中で複数文に分けて書く。
const upsertBatchSize = 1000

type DistributorProductRepository struct {
	db *gorm.DB
}

func NewDistributorProductRepository(db *gorm.DB) *DistributorProductRepository {
	return &DistributorProductRepository{db: db}
}

func (r *DistributorProductRepository) FindByDistributor(ctx context.Context, distributorID shareddomain.ID) ([]*distdomain.DistributorProduct, error) {
	var models []DistributorProductModel
	if err := r.dbOf(ctx).Where("distributor_id = ?", uuid.UUID(distributorID)).Find(&models).Error; err != nil {
		return nil, err
	}
	products := make([]*distdomain.DistributorProduct, 0, len(models))
	for _, m := range models {
		products = append(products, toDomainDistributorProduct(m))
	}
	return products, nil
}

// FindByDistributorAndCode は突合キーで1件引く。見つからない場合は (nil, nil)。
// Firstではなく Limit(1).Find を使い「見つからない」をエラー扱いにしない（ログにも出さない）。
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

// UpsertAll は INSERT … ON CONFLICT (distributor_id, distributor_product_code) DO UPDATE で
// 新規と変更をまとめて書く。既存の行はidを書き換えず、CSVに載る列だけを上書きする
// （unit_priceにNULL＝単価非公表を書き戻すこともある）。
func (r *DistributorProductRepository) UpsertAll(ctx context.Context, products []*distdomain.DistributorProduct) error {
	if len(products) == 0 {
		return nil
	}
	models := make([]DistributorProductModel, 0, len(products))
	for _, p := range products {
		models = append(models, toDistributorProductModel(p))
	}
	return r.dbOf(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "distributor_id"}, {Name: "distributor_product_code"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"name", "vendor_name", "vendor_product_code", "jan_code", "unit_price", "discontinued",
			}),
		}).
		CreateInBatches(&models, upsertBatchSize).Error
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
