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

// Status は取り込み1回分の状態。
type Status string

const (
	// StatusStaged はCSVを中間表現に変換してステージングに保存し終えた状態（まだDBには反映していない）。
	StatusStaged Status = "staged"
	// StatusApplied はステージングの内容を卸商品マスタへ反映し終えた状態。
	StatusApplied Status = "applied"
	// StatusNeedsReview は人手での確認が必要な状態。自動リトライはしない
	// （backend側のdomain-rules.md「卸連携CSV基盤」の失敗時ルール）。
	StatusNeedsReview Status = "needs_review"
)

// IngestionRun はS3オブジェクト1件の取り込み実行。ステージング行を子として持つ集約。
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

// StagingRow はCSV1行を共通の中間表現に正規化したもの。
// 反映できなかった行の原因を人が追えるよう、CSVの原文(raw)も保持する。
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

// StagingFacilityPrice は医院別単価を持つ卸のCSVから読み取った1件分。
// facilityCodeは卸側の医院コードで、こちらのクリニックIDへの突合は反映時に行う。
type StagingFacilityPrice struct {
	FacilityCode string
	UnitPrice    int
}

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

// AddRow は正常に読み取れた行を追加する。
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

// AddInvalidRow は読み取れなかった行を理由付きで追加する。1行の不備で全体を捨てず、
// 最後まで読み切ってから判断できるようにするため、パース段では例外にしない。
func (r *IngestionRun) AddInvalidRow(rowNo int, raw, errorMessage string) {
	r.rows = append(r.rows, StagingRow{
		rowNo:        rowNo,
		raw:          raw,
		valid:        false,
		errorMessage: errorMessage,
	})
}

// ValidRows は反映対象となる行だけを返す。
func (r *IngestionRun) ValidRows() []StagingRow {
	rows := make([]StagingRow, 0, len(r.rows))
	for _, row := range r.rows {
		if row.valid {
			rows = append(rows, row)
		}
	}
	return rows
}

// InvalidRows は読み取れなかった行だけを返す。
func (r *IngestionRun) InvalidRows() []StagingRow {
	rows := make([]StagingRow, 0)
	for _, row := range r.rows {
		if !row.valid {
			rows = append(rows, row)
		}
	}
	return rows
}

// MarkApplied は反映完了として記録する。
func (r *IngestionRun) MarkApplied(finishedAt time.Time) {
	r.status = StatusApplied
	r.message = ""
	r.finishedAt = &finishedAt
}

// MarkNeedsReview は要確認として記録する。反映は行われていない。
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

// ReconstructIngestionRun は永続化データからIngestionRunを復元する。バリデーションは行わない。
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

// ReconstructStagingRow は永続化データからStagingRowを復元する。
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
