package distributorcatalog

import (
	"errors"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// DistributorProduct（卸商品）。取り込みの反映先となる集約。
//
// backend（clinic-inventory）にも同じ集約があり、業務ルール（必須項目・単価の扱い・廃盤）は
// そちらと揃える必要がある。このリポジトリが持つのは取り込みに必要な範囲だけで、
// 参照・検索系の操作は持たない。
//
// 独立した集約ルート（Distributorの配下エンティティにはしない。理由はbackend側の
// docs/architecture/domain-rules.md「卸連携コンテキスト」参照）。
//
// 卸商品コード（DistributorProductCode）は卸独自の商品コード体系。
// ベンダー（メーカー）名・ベンダーが割り当てている商品コード（VendorProductCode）は別途保持する。
//
// 標準単価（unitPrice）はnil可。単価を公表せず商品マスタだけ送ってくる卸があるため、
// 「0円」と「非公表」を区別できるようポインタで持つ（docs/catalog-import-pipeline.md「単価の3パターン」）。
// 医院ごとに単価を決めている卸の単価はここではなくFacilityPriceが持つ。
type DistributorProduct struct {
	id                     shareddomain.ID
	distributorID          shareddomain.ID
	distributorProductCode string
	name                   string
	vendorName             string
	vendorProductCode      string // 任意。ベンダーが割り当てている商品コード
	janCode                string // 任意
	unitPrice              *int   // 標準単価（税抜・円）。その卸の定価。nilは非公表
	discontinued           bool
}

func NewDistributorProduct(distributorID shareddomain.ID, distributorProductCode, name, vendorName string, unitPrice *int) (*DistributorProduct, error) {
	if distributorID.IsZero() {
		return nil, errors.New("卸業者の指定は必須です")
	}
	if distributorProductCode == "" {
		return nil, errors.New("卸商品コードは必須です")
	}
	if name == "" {
		return nil, errors.New("商品名は必須です")
	}
	if vendorName == "" {
		return nil, errors.New("ベンダー名は必須です")
	}
	if unitPrice != nil && *unitPrice <= 0 {
		return nil, errors.New("単価は1円以上で指定してください")
	}
	return &DistributorProduct{
		id:                     shareddomain.NewID(),
		distributorID:          distributorID,
		distributorProductCode: distributorProductCode,
		name:                   name,
		vendorName:             vendorName,
		unitPrice:              unitPrice,
	}, nil
}

func (p *DistributorProduct) ID() shareddomain.ID            { return p.id }
func (p *DistributorProduct) DistributorID() shareddomain.ID { return p.distributorID }
func (p *DistributorProduct) DistributorProductCode() string { return p.distributorProductCode }
func (p *DistributorProduct) Name() string                   { return p.name }
func (p *DistributorProduct) VendorName() string             { return p.vendorName }
func (p *DistributorProduct) VendorProductCode() string      { return p.vendorProductCode }
func (p *DistributorProduct) JANCode() string                { return p.janCode }
func (p *DistributorProduct) UnitPrice() *int                { return p.unitPrice }
func (p *DistributorProduct) Discontinued() bool             { return p.discontinued }

// HasUnitPrice は標準単価が公表されているかを返す。
func (p *DistributorProduct) HasUnitPrice() bool { return p.unitPrice != nil }

func (p *DistributorProduct) SetVendorProductCode(code string) {
	p.vendorProductCode = code
}

func (p *DistributorProduct) SetJANCode(code string) {
	p.janCode = code
}

// Discontinue は卸商品を廃盤にする。物理削除しないのは、クリニック商品からの参照が
// 残っている可能性があるため（参照整合性を壊さない）。
func (p *DistributorProduct) Discontinue() {
	p.discontinued = true
}

// ApplyCatalogUpdate は卸から届いた商品マスタCSVの内容を既存の卸商品に反映する
// （docs/catalog-import-pipeline.md）。卸商品コードと所属卸業者は
// 突合キーそのものなので変更しない。単価nilは「非公表」としてそのまま反映する。
func (p *DistributorProduct) ApplyCatalogUpdate(name, vendorName, vendorProductCode, janCode string, unitPrice *int, discontinued bool) error {
	if name == "" {
		return errors.New("商品名は必須です")
	}
	if vendorName == "" {
		return errors.New("ベンダー名は必須です")
	}
	if unitPrice != nil && *unitPrice <= 0 {
		return errors.New("単価は1円以上で指定してください")
	}
	p.name = name
	p.vendorName = vendorName
	p.vendorProductCode = vendorProductCode
	p.janCode = janCode
	p.unitPrice = unitPrice
	p.discontinued = discontinued
	return nil
}

// ReconstructDistributorProduct は永続化データからDistributorProductを復元する。バリデーションは行わない。
func ReconstructDistributorProduct(
	id shareddomain.ID,
	distributorID shareddomain.ID,
	distributorProductCode, name, vendorName, vendorProductCode, janCode string,
	unitPrice *int,
	discontinued bool,
) *DistributorProduct {
	return &DistributorProduct{
		id:                     id,
		distributorID:          distributorID,
		distributorProductCode: distributorProductCode,
		name:                   name,
		vendorName:             vendorName,
		vendorProductCode:      vendorProductCode,
		janCode:                janCode,
		unitPrice:              unitPrice,
		discontinued:           discontinued,
	}
}
