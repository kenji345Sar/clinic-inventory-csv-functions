package distributorcsvingestion

import (
	"context"
	"fmt"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// DistributorResolver はS3のフォルダ名になっている卸コードを、卸ID(distributors.id)に変換する。
//
// コードとIDの対応はbackend(clinic-inventory)のDBが持ち主のため、設定ファイルに二重に持たず
// 毎回DBを引く。未登録のコードのフォルダにCSVが置かれた場合はここでエラーになり、
// そのファイルは取り込まれない（卸を登録していないのに商品だけ入る、という状態を防ぐ）。
type DistributorResolver struct {
	db *gorm.DB
}

func NewDistributorResolver(db *gorm.DB) *DistributorResolver {
	return &DistributorResolver{db: db}
}

func (r *DistributorResolver) Resolve(ctx context.Context, distributorCode string) (shareddomain.ID, error) {
	var ids []uuid.UUID
	err := r.db.WithContext(ctx).
		Table("distributors").
		Where("code = ?", distributorCode).
		Limit(1).
		Pluck("id", &ids).Error
	if err != nil {
		return shareddomain.ID{}, err
	}
	if len(ids) == 0 {
		return shareddomain.ID{}, fmt.Errorf("卸コード %s に対応する卸業者が登録されていません", distributorCode)
	}
	return shareddomain.ID(ids[0]), nil
}
