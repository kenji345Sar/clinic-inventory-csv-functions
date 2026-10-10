package distributorcatalog

import (
	"errors"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// FacilityPrice は、卸が医院（クリニック）ごとに決めている卸商品の単価を表す。
//
// 【医院別単価の識別】
// ・卸商品とクリニックの組み合わせで、同じ医院別単価を判別する。
// ・1つの卸商品・1つのクリニックにつき、単価は1つだけ。
// ・この組み合わせで決まるので、独自のシステム内部IDは持たない。
//
// 【単価】
// ・税抜・円で管理する。税込からの割り戻しはしないので、卸が税抜で送ることが前提。
// ・1円以上とする。0円やマイナス金額は認めない。
// ・卸商品の標準単価（DistributorProduct）とは別に持つ。医院ごとに単価を決めている卸の商品は、
// 標準単価が未設定で、単価はここにだけある（docs/catalog-import-pipeline.md「単価の3パターン」）。
//
// 【医院別単価が無くなったとき】
// ・卸のCSVからある医院の単価が消えても、削除しない。単価CSVの欠落だけで契約終了とは判断できない
// （扱いを変えるかは未決。docs/catalog-import-pipeline.md 3-2）。
//
// 【集約の範囲】
// ・卸商品（DistributorProduct）の配下ではなく、別の集約として扱う。単価は卸とクリニックの契約に
// 紐づく情報で、商品マスタとは別のタイミング・別のCSVで届くことがあるため、商品と切り離して更新できるようにする。
type FacilityPrice struct {
	distributorProductID shareddomain.ID
	facilityID           shareddomain.ID
	unitPrice            int
}

// NewFacilityPrice は、医院別単価を作る。
//
// 【作成のルール】
// ・卸商品の指定は必須。
// ・クリニックの指定は必須。卸側の医院コードではなく、こちらのクリニックIDに変換済みの値を渡す。
// ・単価は1円以上。
// ・既に同じ卸商品・クリニックの単価があれば、保存時に単価が上書きされる。
func NewFacilityPrice(distributorProductID, facilityID shareddomain.ID, unitPrice int) (*FacilityPrice, error) {
	if distributorProductID.IsZero() {
		return nil, errors.New("卸商品の指定は必須です")
	}
	if facilityID.IsZero() {
		return nil, errors.New("クリニックの指定は必須です")
	}
	if err := ValidateFacilityUnitPrice(unitPrice); err != nil {
		return nil, err
	}
	return &FacilityPrice{
		distributorProductID: distributorProductID,
		facilityID:           facilityID,
		unitPrice:            unitPrice,
	}, nil
}

// ValidateFacilityUnitPrice は、医院別単価が業務ルールを満たすかを確かめる。
//
// 【確かめるルール】
// ・単価は1円以上。
//
// 【呼ばれる場所】
// ・取り込みで、ステージングに積む前と、NewFacilityPrice の中（理由は ValidateCatalogValues と同じ）。
func ValidateFacilityUnitPrice(unitPrice int) error {
	if unitPrice <= 0 {
		return errors.New("医院別単価は1円以上で指定してください")
	}
	return nil
}

func (p *FacilityPrice) DistributorProductID() shareddomain.ID { return p.distributorProductID }
func (p *FacilityPrice) FacilityID() shareddomain.ID           { return p.facilityID }
func (p *FacilityPrice) UnitPrice() int                        { return p.unitPrice }

// ReconstructFacilityPrice は、DBに保存されている医院別単価を復元する。
//
// 【復元のルール】
// ・業務ルールのチェックはしない（理由は ReconstructDistributorProduct と同じ）。
func ReconstructFacilityPrice(distributorProductID, facilityID shareddomain.ID, unitPrice int) *FacilityPrice {
	return &FacilityPrice{
		distributorProductID: distributorProductID,
		facilityID:           facilityID,
		unitPrice:            unitPrice,
	}
}
