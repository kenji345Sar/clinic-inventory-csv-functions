package distributorcatalog

import (
	"errors"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// FacilityPrice（医院別単価）。卸によっては商品ごとの定価ではなく、
// 医院（クリニック）ごとに個別の単価を設定している（docs/design.md「単価の3パターン」）。
//
// DistributorProductの配下エンティティにはしない。単価は卸とクリニックの契約に紐づく情報で、
// 商品マスタの更新とは別のタイミング・別のCSVで届くことがあるため、独立して更新できる形にする。
// 同一性は(卸商品, クリニック)の組で決まるため、独自のIDは持たない。
type FacilityPrice struct {
	distributorProductID shareddomain.ID
	facilityID           shareddomain.ID
	unitPrice            int
}

func NewFacilityPrice(distributorProductID, facilityID shareddomain.ID, unitPrice int) (*FacilityPrice, error) {
	if distributorProductID.IsZero() {
		return nil, errors.New("卸商品の指定は必須です")
	}
	if facilityID.IsZero() {
		return nil, errors.New("クリニックの指定は必須です")
	}
	if unitPrice <= 0 {
		return nil, errors.New("医院別単価は1円以上で指定してください")
	}
	return &FacilityPrice{
		distributorProductID: distributorProductID,
		facilityID:           facilityID,
		unitPrice:            unitPrice,
	}, nil
}

func (p *FacilityPrice) DistributorProductID() shareddomain.ID { return p.distributorProductID }
func (p *FacilityPrice) FacilityID() shareddomain.ID           { return p.facilityID }
func (p *FacilityPrice) UnitPrice() int                        { return p.unitPrice }

// ReconstructFacilityPrice は永続化データからFacilityPriceを復元する。バリデーションは行わない。
func ReconstructFacilityPrice(distributorProductID, facilityID shareddomain.ID, unitPrice int) *FacilityPrice {
	return &FacilityPrice{
		distributorProductID: distributorProductID,
		facilityID:           facilityID,
		unitPrice:            unitPrice,
	}
}
