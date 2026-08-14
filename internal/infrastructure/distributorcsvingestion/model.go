package distributorcsvingestion

import (
	"time"

	"github.com/google/uuid"
)

// このファイルは取り込み履歴・ステージングテーブルの列マッピング（gorm）。
// **テーブルの定義（作成・列変更）はこのリポジトリでは行わない**。スキーマの所有者は
// backend（clinic-inventory）で、テーブルはbackend側のAutoMigrateで作られる。

// IngestionRunModel は取り込み1回分の実行記録（gorm）。
// (s3_key, etag) の複合indexは「同じキー・同じ内容は取り込み済み」の判定に使う。
type IngestionRunModel struct {
	ID            uuid.UUID `gorm:"type:uuid;primaryKey"`
	DistributorID uuid.UUID `gorm:"type:uuid;not null;index"`
	S3Key         string    `gorm:"not null;index:idx_ingestion_runs_key_etag"`
	ETag          string    `gorm:"column:etag;index:idx_ingestion_runs_key_etag"`
	Status        string    `gorm:"not null;index"`
	Message       string
	StartedAt     time.Time `gorm:"not null"`
	FinishedAt    *time.Time
}

func (IngestionRunModel) TableName() string { return "distributor_catalog_ingestion_runs" }

// IngestionStagingRowModel はCSV1行を正規化した中間表現の保存先（gorm）。
// 同一性は(取り込み実行, 行番号)で決まるため、この2列を複合主キーにしている。
type IngestionStagingRowModel struct {
	IngestionRunID uuid.UUID `gorm:"type:uuid;primaryKey"`
	RowNo          int       `gorm:"primaryKey"`
	// RawはCSVの原文。突合に失敗した行の原因を人が追えるように残す。
	Raw                    string
	Valid                  bool `gorm:"not null"`
	ErrorMessage           string
	DistributorProductCode string `gorm:"index"`
	Name                   string
	VendorName             string
	VendorProductCode      string
	JANCode                string `gorm:"column:jan_code"`
	UnitPrice              *int   // NULLは単価非公表
	// FacilityPrices は医院別単価をJSON文字列で持つ。1行に可変個ぶら下がる値で、
	// ステージングは「反映前に人が確認するための一時置き場」のため正規化までは行わない。
	FacilityPrices string `gorm:"type:text"`
	Discontinued   bool   `gorm:"not null;default:false"`
}

func (IngestionStagingRowModel) TableName() string { return "distributor_catalog_staging_rows" }
