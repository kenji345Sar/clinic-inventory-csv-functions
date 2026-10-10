package distributorcsvingestion

import (
	"context"

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

// ResolveAll は医院コードをまとめて1回の問い合わせで変換する。対応するクリニックが無いコード
// （UUIDでない・facilitiesに無い）は戻り値のmapに含めない。どの行のどのコードかを
// 人に示せるのは呼び出し側なので、エラーにはせず呼び出し側に判断させる。
func (r *FacilityResolver) ResolveAll(ctx context.Context, distributorID shareddomain.ID, facilityCodes []string) (map[string]shareddomain.ID, error) {
	codeByID := make(map[uuid.UUID]string, len(facilityCodes))
	for _, code := range facilityCodes {
		if id, err := uuid.Parse(code); err == nil {
			codeByID[id] = code
		}
	}
	resolved := make(map[string]shareddomain.ID, len(codeByID))
	if len(codeByID) == 0 {
		return resolved, nil
	}
	ids := make([]uuid.UUID, 0, len(codeByID))
	for id := range codeByID {
		ids = append(ids, id)
	}
	var found []uuid.UUID
	if err := r.db.WithContext(ctx).
		Table("facilities").
		Where("id IN ?", ids).
		Pluck("id", &found).Error; err != nil {
		return nil, err
	}
	for _, id := range found {
		resolved[codeByID[id]] = shareddomain.ID(id)
	}
	return resolved, nil
}
