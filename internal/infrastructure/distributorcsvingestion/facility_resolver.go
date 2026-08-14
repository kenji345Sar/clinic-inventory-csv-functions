package distributorcsvingestion

import (
	"context"
	"fmt"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// FacilityResolver は医院別単価CSVに載っている医院コードを、クリニックID（facilities.id）に変換する。
//
// 現状は「医院コード = クリニックID(UUID)」として扱い、実在確認だけを行う。実際の卸は自社の
// 医院コード体系を使うため、本番運用では (卸業者, 卸側医院コード) → クリニックID の対応表が
// 別途必要になる。その対応表をどこで持つかは未決のため、まずは変換をこのアダプタ1か所に
// 閉じ込めておき、決まり次第ここだけを差し替える。
//
// クリニック自体の業務ルールはbackend（clinic-inventory）の責務のため、こちらでは
// organizationのドメインを持たず、実在するかどうかだけをDBに問い合わせる。
type FacilityResolver struct {
	db *gorm.DB
}

func NewFacilityResolver(db *gorm.DB) *FacilityResolver {
	return &FacilityResolver{db: db}
}

func (r *FacilityResolver) Resolve(ctx context.Context, distributorID shareddomain.ID, facilityCode string) (shareddomain.ID, error) {
	id, err := uuid.Parse(facilityCode)
	if err != nil {
		return shareddomain.ID{}, fmt.Errorf("医院コード %s をクリニックに突合できません（現状はクリニックIDのUUIDのみ対応）", facilityCode)
	}
	var count int64
	if err := r.db.WithContext(ctx).
		Table("facilities").
		Where("id = ?", id).
		Count(&count).Error; err != nil {
		return shareddomain.ID{}, err
	}
	if count == 0 {
		return shareddomain.ID{}, fmt.Errorf("医院コード %s に対応するクリニックが見つかりません", facilityCode)
	}
	return shareddomain.ID(id), nil
}
