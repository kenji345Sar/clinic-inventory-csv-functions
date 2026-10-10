package distributorcatalog

import (
	"context"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// このリポジトリが必要とするのは「商品マスタCSVを反映する」ための操作だけ。
// 検索・一覧といった参照系はbackend（clinic-inventory）のAPIが担うため、ここには持たない。
type DistributorProductRepository interface {
	// FindByDistributor はその卸の商品を全件引く。取り込みではCSVの全行と突き合わせるため、
	// 商品ごとに引かず1回で読む。
	FindByDistributor(ctx context.Context, distributorID shareddomain.ID) ([]*DistributorProduct, error)
	// FindByDistributorAndCode は突合キー(卸業者, 卸商品コード)で1件引く。
	// 見つからない場合は (nil, nil) を返す。
	FindByDistributorAndCode(ctx context.Context, distributorID shareddomain.ID, code string) (*DistributorProduct, error)
	// UpsertAll は新規・変更のあった商品をまとめて書く。(卸業者, 卸商品コード)が既にあれば
	// 上書きし、無ければ挿入する。
	UpsertAll(ctx context.Context, products []*DistributorProduct) error
}

// FacilityPriceRepository は医院別単価の反映を担う。
type FacilityPriceRepository interface {
	// FindByDistributor はその卸の商品に付いている医院別単価を全件引く。
	FindByDistributor(ctx context.Context, distributorID shareddomain.ID) ([]*FacilityPrice, error)
	// UpsertAll は医院別単価をまとめて登録・更新する。
	UpsertAll(ctx context.Context, prices []*FacilityPrice) error
}
