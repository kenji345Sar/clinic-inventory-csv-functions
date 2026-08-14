package distributorcatalog

import (
	"context"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// このリポジトリが必要とするのは「商品マスタCSVを反映する」ための操作だけ。
// 検索・一覧といった参照系はbackend（clinic-inventory）のAPIが担うため、ここには持たない。
type DistributorProductRepository interface {
	// FindByDistributorAndCode は突合キー(卸業者, 卸商品コード)で1件引く。
	// 見つからない場合は (nil, nil) を返す（取り込みでは「無い＝新規行」が正常系のため）。
	FindByDistributorAndCode(ctx context.Context, distributorID shareddomain.ID, code string) (*DistributorProduct, error)
	Create(ctx context.Context, product *DistributorProduct) error
	Update(ctx context.Context, product *DistributorProduct) error
}

// FacilityPriceRepository は医院別単価の反映を担う。
type FacilityPriceRepository interface {
	// UpsertAll は医院別単価をまとめて登録・更新する。CSVでは1商品につき
	// 契約医院数分の行がまとめて届くため、1件ずつではなくまとめて受け取る。
	UpsertAll(ctx context.Context, prices []*FacilityPrice) error
}
