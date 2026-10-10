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
	// Save は取り込み実行とステージング行をまとめて保存する（新規・更新の両方）。
	Save(ctx context.Context, run *IngestionRun) error
	// FindByDistributor は、その卸の取り込みの記録を、新しい順にステージング行ごと返す。
	FindByDistributor(ctx context.Context, distributorID shareddomain.ID) ([]*IngestionRun, error)
}
