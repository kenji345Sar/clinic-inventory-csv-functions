// Package distributorcsvingestion は卸連携CSV基盤コンテキスト
// (backend側のdocs/architecture/domain-rules.md「卸連携CSV基盤」)のうち、
// 取り込み1回分の実行記録(IngestionRun)と、正規化した中間表現の置き場である
// ステージング行(StagingRow)を扱う。
//
// このコンテキスト自体は商品・単価という業務データを持たない。CSVを共通の中間表現に
// 正規化してここに溜め、その内容をDistributorCatalogコンテキストの集約へ反映する
// (docs/catalog-import-pipeline.md「全体の流れ」)。
package distributorcsvingestion

import (
	"errors"
	"time"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// Status は、取り込み1回分の状態を表す。
//
// 【状態の移り変わり】
// ・作成時は staged。
// ・反映し終えたら applied、不備や反映の失敗があれば needs_review にする。
// ・applied と needs_review は終わりの状態で、そこから別の状態へは移さない
// （移さないのはユースケースの作りによるもので、この型は遷移を制限していない）。
type Status string

const (
	// StatusStaged は、CSVを中間表現に変換してステージングに保存し終えた状態。卸商品マスタにはまだ反映していない。
	StatusStaged Status = "staged"
	// StatusApplied は、卸商品マスタへの反映が終わった状態。
	// 同じS3キー・同じETagのCSVは、applied の取り込みがあれば次から取り込まない。
	StatusApplied Status = "applied"
	// StatusNeedsReview は、人が確認する必要がある状態。卸商品マスタには何も反映していない。
	// 理由は message と、ステージング行の error_message に残る。
	// 方針は「自動リトライしない」（backend側のdomain-rules.md「卸連携CSV基盤」）だが、
	// 二重取り込みの判定は applied しか見ないため、同じCSVが置かれたままなら次の実行で再び取り込まれる。
	StatusNeedsReview Status = "needs_review"
)

// IngestionRun は、S3に置かれたCSV1ファイルを取り込んだ1回分の記録を表す。
//
// 【取り込み1回の識別】
// ・1回の取り込みごとにシステム内部IDを発行する。同じCSVを取り込み直せば、別の記録になる。
// ・どの卸の、どのS3キーの、どの内容（ETag）を取り込んだかを持つ。
//
// 【二重取り込みの防止】
// ・同じS3キー・同じETagで applied の記録があれば、そのCSVは取り込まない。
// ・ETagは中身で決まるので、CSVを置き直して中身が変われば取り込み直される。
// ・以前と同じ中身に戻したCSVは、ETagが過去と一致するので取り込まれない
// （docs/catalog-import-pipeline.md 5章）。
//
// 【ステージング行】
// ・CSVの行（卸Bのように商品単位にまとめるパーサでは商品）ごとに1行、子として持つ。
// ・読み取れなかった行・業務ルールに合わない行も、理由付きで持つ。捨てると、要確認になったとき
// どの行がなぜ駄目だったかを後から追えない。
// ・不備のある行が1行でもあれば、その取り込みは反映しない。
//
// 【集約の範囲】
// ・取り込みの記録とステージング行をこの集約で管理する。商品・単価という業務データは持たず、
// 反映先は卸商品（DistributorProduct）と医院別単価（FacilityPrice）の集約。
type IngestionRun struct {
	id            shareddomain.ID
	distributorID shareddomain.ID
	s3Key         string
	// etag はS3オブジェクトの内容から決まる識別子。キーが同じでも中身が変われば変わるため、
	// 「同じキー・同じETagは取り込み済み」という冪等性の判定に使う。
	etag       string
	status     Status
	message    string // 要確認になった理由。正常時は空
	startedAt  time.Time
	finishedAt *time.Time
	rows       []StagingRow
}

// StagingRow は、取り込んだCSVの1行を、卸ごとの形式差を除いた形で記録したものを表す。
//
// 【記録する内容】
// ・CSV上の行番号と、CSVの原文（raw）を必ず持つ。原文があれば、反映できなかった行を人がCSVと照らし合わせられる。
// ・正常に読めた行は、卸商品と医院別単価に反映する値を持つ。
// ・不備のある行は、値の代わりに不備の理由を持つ。
// ・医院別単価の医院コードは卸側のコードのまま持つ。クリニックIDへの変換は反映のときに使う。
type StagingRow struct {
	rowNo                  int // CSV上の行番号（1始まり・ヘッダを含む）
	raw                    string
	valid                  bool
	errorMessage           string
	distributorProductCode string
	name                   string
	vendorName             string
	vendorProductCode      string
	janCode                string
	unitPrice              *int // nilは単価非公表
	facilityPrices         []StagingFacilityPrice
	discontinued           bool
}

// StagingFacilityPrice は、医院別単価を持つ卸のCSVから読み取った1件分を表す。
//
// 【記録する内容】
// ・医院コードは卸側のコードのまま持つ。こちらのクリニックIDへの変換は反映のときに使う。
// ・単価はCSVの値のまま持つ。1円以上かどうかは、ステージングに積む前に確かめ済み。
type StagingFacilityPrice struct {
	FacilityCode string
	UnitPrice    int
}

// NewIngestionRun は、取り込み1回分の記録を始める。
//
// 【作成のルール】
// ・卸業者の指定は必須。
// ・S3キーは必須。
// ・ETagは空でもよい。
// ・状態は staged で作成する。
// ・システム内部IDは新しく発行する。
func NewIngestionRun(distributorID shareddomain.ID, s3Key, etag string, startedAt time.Time) (*IngestionRun, error) {
	if distributorID.IsZero() {
		return nil, errors.New("卸業者の指定は必須です")
	}
	if s3Key == "" {
		return nil, errors.New("S3キーは必須です")
	}
	return &IngestionRun{
		id:            shareddomain.NewID(),
		distributorID: distributorID,
		s3Key:         s3Key,
		etag:          etag,
		status:        StatusStaged,
		startedAt:     startedAt,
	}, nil
}

func (r *IngestionRun) ID() shareddomain.ID            { return r.id }
func (r *IngestionRun) DistributorID() shareddomain.ID { return r.distributorID }
func (r *IngestionRun) S3Key() string                  { return r.s3Key }
func (r *IngestionRun) ETag() string                   { return r.etag }
func (r *IngestionRun) Status() Status                 { return r.status }
func (r *IngestionRun) Message() string                { return r.message }
func (r *IngestionRun) StartedAt() time.Time           { return r.startedAt }
func (r *IngestionRun) FinishedAt() *time.Time         { return r.finishedAt }
func (r *IngestionRun) Rows() []StagingRow             { return r.rows }

// AddRow は、正常に読み取れた行をステージング行として追加する。
//
// 【追加のルール】
// ・反映の対象になる行として追加する。
// ・業務ルールは呼び出し側で確かめ済みの前提で、ここでは確かめない。
func (r *IngestionRun) AddRow(
	rowNo int,
	raw string,
	distributorProductCode, name, vendorName, vendorProductCode, janCode string,
	unitPrice *int,
	facilityPrices []StagingFacilityPrice,
	discontinued bool,
) {
	r.rows = append(r.rows, StagingRow{
		rowNo:                  rowNo,
		raw:                    raw,
		valid:                  true,
		distributorProductCode: distributorProductCode,
		name:                   name,
		vendorName:             vendorName,
		vendorProductCode:      vendorProductCode,
		janCode:                janCode,
		unitPrice:              unitPrice,
		facilityPrices:         facilityPrices,
		discontinued:           discontinued,
	})
}

// AddInvalidRow は、不備のある行を理由付きでステージング行として追加する。
//
// 【追加のルール】
// ・反映の対象にならない行として追加する。
// ・理由（errorMessage）と原文（raw）を必ず残す。
// ・1行の不備で取り込み全体を捨てず、最後まで読み切ってから止めるかを判断する。
// その場で止めると、2行目以降の不備が分からず、直して置き直すたびに次の不備が見つかる。
func (r *IngestionRun) AddInvalidRow(rowNo int, raw, errorMessage string) {
	r.rows = append(r.rows, StagingRow{
		rowNo:        rowNo,
		raw:          raw,
		valid:        false,
		errorMessage: errorMessage,
	})
}

// ValidRows は、反映の対象になる行だけを返す。
func (r *IngestionRun) ValidRows() []StagingRow {
	rows := make([]StagingRow, 0, len(r.rows))
	for _, row := range r.rows {
		if row.valid {
			rows = append(rows, row)
		}
	}
	return rows
}

// InvalidRows は、不備のある行だけを返す。1行でもあれば、その取り込みは反映しない。
func (r *IngestionRun) InvalidRows() []StagingRow {
	rows := make([]StagingRow, 0)
	for _, row := range r.rows {
		if !row.valid {
			rows = append(rows, row)
		}
	}
	return rows
}

// MarkApplied は、卸商品マスタへの反映が終わったことを記録する。
//
// 【記録のルール】
// ・状態を applied にし、終了時刻を記録する。
// ・前に要確認の理由が入っていても消す。
func (r *IngestionRun) MarkApplied(finishedAt time.Time) {
	r.status = StatusApplied
	r.message = ""
	r.finishedAt = &finishedAt
}

// MarkNeedsReview は、人が確認する必要があることを記録する。
//
// 【記録のルール】
// ・状態を needs_review にし、理由と終了時刻を記録する。
// ・呼ぶのは、不備のある行があったとき、反映が失敗したとき、CSVを読めなかったとき。
// ・どの場合も、卸商品マスタには何も反映されていない（反映の失敗はロールバック済み）。
func (r *IngestionRun) MarkNeedsReview(reason string, finishedAt time.Time) {
	r.status = StatusNeedsReview
	r.message = reason
	r.finishedAt = &finishedAt
}

func (row StagingRow) RowNo() int                             { return row.rowNo }
func (row StagingRow) Raw() string                            { return row.raw }
func (row StagingRow) Valid() bool                            { return row.valid }
func (row StagingRow) ErrorMessage() string                   { return row.errorMessage }
func (row StagingRow) DistributorProductCode() string         { return row.distributorProductCode }
func (row StagingRow) Name() string                           { return row.name }
func (row StagingRow) VendorName() string                     { return row.vendorName }
func (row StagingRow) VendorProductCode() string              { return row.vendorProductCode }
func (row StagingRow) JANCode() string                        { return row.janCode }
func (row StagingRow) UnitPrice() *int                        { return row.unitPrice }
func (row StagingRow) FacilityPrices() []StagingFacilityPrice { return row.facilityPrices }
func (row StagingRow) Discontinued() bool                     { return row.discontinued }

// ReconstructIngestionRun は、DBに保存されている取り込みの記録を復元する。
//
// 【復元のルール】
// ・業務ルールのチェックはしない。DBにある値は記録のときに確かめ済み。
func ReconstructIngestionRun(
	id, distributorID shareddomain.ID,
	s3Key, etag string,
	status Status,
	message string,
	startedAt time.Time,
	finishedAt *time.Time,
	rows []StagingRow,
) *IngestionRun {
	return &IngestionRun{
		id:            id,
		distributorID: distributorID,
		s3Key:         s3Key,
		etag:          etag,
		status:        status,
		message:       message,
		startedAt:     startedAt,
		finishedAt:    finishedAt,
		rows:          rows,
	}
}

// ReconstructStagingRow は、DBに保存されているステージング行を復元する。
//
// 【復元のルール】
// ・業務ルールのチェックはしない。
func ReconstructStagingRow(
	rowNo int,
	raw string,
	valid bool,
	errorMessage string,
	distributorProductCode, name, vendorName, vendorProductCode, janCode string,
	unitPrice *int,
	facilityPrices []StagingFacilityPrice,
	discontinued bool,
) StagingRow {
	return StagingRow{
		rowNo:                  rowNo,
		raw:                    raw,
		valid:                  valid,
		errorMessage:           errorMessage,
		distributorProductCode: distributorProductCode,
		name:                   name,
		vendorName:             vendorName,
		vendorProductCode:      vendorProductCode,
		janCode:                janCode,
		unitPrice:              unitPrice,
		facilityPrices:         facilityPrices,
		discontinued:           discontinued,
	}
}
