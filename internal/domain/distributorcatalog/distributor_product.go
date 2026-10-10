package distributorcatalog

import (
	"errors"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// DistributorProduct は、卸業者が取り扱う商品を表す。
//
// 【卸商品の識別】
// ・卸業者と卸商品コードの組み合わせで、同じ卸商品を判別する。
// ・卸業者と卸商品コードは必須で、登録後は変更しない。変えると次の取り込みで同じ商品を
// 見つけられず、別の商品として二重に登録される。
// ・卸商品コードは卸業者独自のコードであり、ベンダー（メーカー）の商品コードとは異なる。
//
// 【商品情報】
// ・商品名とベンダー名は必須とする。
// ・ベンダー商品コードとJANコードは未設定でもよい。
//
// 【標準単価】
// ・標準単価は税抜・円で管理する。税込からの割り戻しはしないので、卸が税抜で送ることが前提。
// ・標準単価を設定する場合は1円以上とする。0円やマイナス金額は認めない。
// ・標準単価を公表しない卸業者があるため、単価未設定を認める。
// ・0円は持てないので、単価が無いことは未設定（nil）だけで表す。0円で持てると、
// 非公表なのか無料なのか区別できず、その単価で発注した金額が0円で計上される。
// ・医院ごとに異なる単価は、卸商品の標準単価ではなく医院別単価（FacilityPrice）で管理する。
//
// 【廃盤】
// ・卸商品は廃盤状態を持つ。
// ・廃盤になっても商品情報を削除せず、廃盤として記録する。削除するとクリニック商品からの
// 参照が切れる。
// ・卸の商品マスタで廃盤が解除された場合は、廃盤を解除できる。
// 廃盤の商品を発注で止める処理は今は無い（docs/catalog-import-backend.md 4章の未決事項）。
//
// 【集約の範囲】
// ・卸商品自身の情報と状態を、このDistributorProductで管理する。
// ・卸業者（Distributor）とは別の集約として扱い、卸業者とは独立して登録・更新する。
// ・医院別単価（FacilityPrice）は別の集約として管理する。
//
// 【backendとの関係】
// ・backend（clinic-inventory）にも同じ集約がある。業務ルールを変えるときは両方を揃える。
// 片方だけ変えると、取り込みで入った商品をbackendの画面で更新できない、といった食い違いが起きる。
// ・このリポジトリが持つのは取り込みに必要な操作だけで、参照・検索はbackendが担う。
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

// NewDistributorProduct は、新しい卸商品を登録するためのオブジェクトを作る。
//
// 【新規登録のルール】
// ・卸業者の指定は必須。
// ・卸商品コードは必須。
// ・商品名とベンダー名は必須。
// ・標準単価は未設定でもよいが、設定する場合は1円以上。
// ・新規登録時は廃盤ではない状態で作成する。卸の商品マスタで既に廃盤だった場合は、
// 作成後に Discontinue で廃盤にする。
// ・商品を識別するシステム内部IDは新しく発行する。
// ・ベンダー商品コードとJANコードはここでは設定しない。作成後に SetVendorProductCode /
// SetJANCode で設定する。
func NewDistributorProduct(distributorID shareddomain.ID, distributorProductCode, name, vendorName string, unitPrice *int) (*DistributorProduct, error) {
	if distributorID.IsZero() {
		return nil, errors.New("卸業者の指定は必須です")
	}
	if distributorProductCode == "" {
		return nil, errors.New("卸商品コードは必須です")
	}
	if err := ValidateCatalogValues(name, vendorName, unitPrice); err != nil {
		return nil, err
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

// ValidateCatalogValues は、卸商品の値が業務ルールを満たすかを確かめる。
//
// 【確かめるルール】
// ・商品名とベンダー名は必須。
// ・標準単価は未設定でもよいが、設定する場合は1円以上。
//
// 【呼ばれる場所】
// ・取り込みで、ステージングに積む前。引っかかった行は理由付きでステージングに残る。
// 反映の途中で見つけると、ファイル全体がロールバックされ、理由も最初の1件しか残らない。
// ・NewDistributorProduct と UpdateFromDistributorCatalog の中。呼び出し側が確かめ忘れても、
// 不正な値の卸商品は作られない。
// ・ルールを1か所に置き、どちらからも同じものを呼ぶ。別々に書くと、片方だけ直して
// 取り込み前は通るのに反映で落ちる、といった食い違いが起きる。
func ValidateCatalogValues(name, vendorName string, unitPrice *int) error {
	if name == "" {
		return errors.New("商品名は必須です")
	}
	if vendorName == "" {
		return errors.New("ベンダー名は必須です")
	}
	if unitPrice != nil && *unitPrice <= 0 {
		return errors.New("単価は1円以上で指定してください")
	}
	return nil
}

// 以下は値を読むためのメソッド。
//
// 【値の変え方】
// ・商品名・ベンダー名・標準単価・廃盤を個別に変えるメソッドは持たない。
// 変えるのは卸の商品マスタが届いたときだけで、UpdateFromDistributorCatalog でまとめて反映する。
// ・卸業者と卸商品コード、システム内部IDは登録後に変えない（変える手段も無い）。
func (p *DistributorProduct) ID() shareddomain.ID            { return p.id }
func (p *DistributorProduct) DistributorID() shareddomain.ID { return p.distributorID }
func (p *DistributorProduct) DistributorProductCode() string { return p.distributorProductCode }
func (p *DistributorProduct) Name() string                   { return p.name }
func (p *DistributorProduct) VendorName() string             { return p.vendorName }
func (p *DistributorProduct) VendorProductCode() string      { return p.vendorProductCode }
func (p *DistributorProduct) JANCode() string                { return p.janCode }
func (p *DistributorProduct) UnitPrice() *int                { return p.unitPrice }
func (p *DistributorProduct) Discontinued() bool             { return p.discontinued }

// HasUnitPrice は、標準単価が公表されているかを返す。
//
// 【判定のルール】
// ・単価未設定（非公表）なら false、1円以上の単価があれば true。
// ・0円の単価は持てないので、「単価が0か」ではなくこれで判定する。
func (p *DistributorProduct) HasUnitPrice() bool { return p.unitPrice != nil }

// SetVendorProductCode は、ベンダー（メーカー）が割り当てている商品コードを設定する。
//
// 【設定のルール】
// ・任意項目。空文字は未設定を表す。
// ・新規登録時に、NewDistributorProduct の後で呼ぶ。
// ・形式はチェックしない。
func (p *DistributorProduct) SetVendorProductCode(code string) {
	p.vendorProductCode = code
}

// SetJANCode は、JANコードを設定する。
//
// 【設定のルール】
// ・任意項目。空文字は未設定を表す。
// ・新規登録時に、NewDistributorProduct の後で呼ぶ。
// ・桁数やチェックディジットは確かめない。
func (p *DistributorProduct) SetJANCode(code string) {
	p.janCode = code
}

// Discontinue は、卸商品を廃盤にする。
//
// 【廃盤のルール】
// ・商品情報は削除せず、廃盤状態として残す。削除するとクリニック商品からの参照が切れる。
// ・新規登録した商品が卸の商品マスタで既に廃盤だった場合に、NewDistributorProduct の直後に呼ぶ。
// ・既存の商品の廃盤・解除は UpdateFromDistributorCatalog が行う。
func (p *DistributorProduct) Discontinue() {
	p.discontinued = true
}

// UpdateFromDistributorCatalog は、卸から届いた商品マスタCSVの内容を既存の卸商品に反映する
// （docs/catalog-csv-to-db-flow.md 6章）。
//
// 【更新のルール】
// ・商品名とベンダー名は必須。
// ・標準単価は未設定（非公表）か1円以上。未設定はそのまま反映し、前回の単価は消える。
// ・卸業者と卸商品コードは変更しない。CSVの行と既存の卸商品を結びつける突合キーなので、
// 変えると次の取り込みで同じ商品を見つけられなくなる。
// ・廃盤状態も含めて商品情報を更新する。CSVで廃盤が0に戻れば廃盤は解除される。
//
// 【変更の判定】
// ・CSVの値と現在の値を比べ、1つでも違えば changed = true を返す。
// ・changed が false なら、呼び出し側は書き込みを省く。変わっていない商品まで書くと、
// 毎回CSVの全件をUPDATEすることになり、何が変わったのかも件数から読めなくなる。
func (p *DistributorProduct) UpdateFromDistributorCatalog(name, vendorName, vendorProductCode, janCode string, unitPrice *int, discontinued bool) (changed bool, err error) {
	if err := ValidateCatalogValues(name, vendorName, unitPrice); err != nil {
		return false, err
	}
	changed = p.name != name ||
		p.vendorName != vendorName ||
		p.vendorProductCode != vendorProductCode ||
		p.janCode != janCode ||
		!samePrice(p.unitPrice, unitPrice) ||
		p.discontinued != discontinued
	p.name = name
	p.vendorName = vendorName
	p.vendorProductCode = vendorProductCode
	p.janCode = janCode
	p.unitPrice = unitPrice
	p.discontinued = discontinued
	return changed, nil
}

// samePrice は、2つの標準単価が同じかを返す。
//
// 【判定のルール】
// ・未設定同士は同じとみなす。
// ・未設定と単価ありは違うとみなす（非公表から公表に変わったのは変更）。
func samePrice(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ReconstructDistributorProduct は、DBに保存されている卸商品を復元する。
//
// 【復元のルール】
// ・業務ルールのチェックはしない。DBにある値は登録・更新のときに確かめ済み。
// ・ここで確かめると、ルールを厳しくしたときに、それ以前に登録した商品がDBから読めなくなる。
// ・システム内部IDは新しく発行せず、DBの値をそのまま使う。
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
