package distributorcsvingestion

import (
	"context"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// IngestionRunRepository は、取り込みの記録を保存・取得する。
type IngestionRunRepository interface {
	// IsAlreadyApplied は、同じS3キー・同じ内容(ETag)が既に反映済みかを返す。
	// 定期実行では同じオブジェクトを何度も一覧するため、ここで二重取り込みを防ぐ。
	// 見るのは applied の記録だけで、needs_review の記録があっても false を返す。
	IsAlreadyApplied(ctx context.Context, s3Key, etag string) (bool, error)
	// Create は、取り込みの記録をステージング行ごと新しく保存する。1回の取り込みにつき1回だけ呼ぶ。
	Create(ctx context.Context, run *IngestionRun) error
	// UpdateStatus は、取り込みの記録の状態・理由・終了時刻だけを保存する。
	// ステージング行は記録した後で変わらないので書かない。行まで書き直すと、
	// 状態を1つ変えるだけでCSVの行数ぶんの削除と挿入が走る。
	UpdateStatus(ctx context.Context, run *IngestionRun) error
	// FindByDistributor は、その卸の取り込みの記録を、新しい順にステージング行ごと返す。
	FindByDistributor(ctx context.Context, distributorID shareddomain.ID) ([]*IngestionRun, error)
}
