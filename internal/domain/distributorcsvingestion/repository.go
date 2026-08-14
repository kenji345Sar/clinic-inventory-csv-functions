package distributorcsvingestion

import (
	"context"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

type IngestionRunRepository interface {
	// IsAlreadyApplied は同じS3キー・同じ内容(ETag)が既に反映済みかを返す。
	// 定期実行では同じオブジェクトを何度も一覧するため、ここで二重取り込みを防ぐ。
	IsAlreadyApplied(ctx context.Context, s3Key, etag string) (bool, error)
	// Save は取り込み実行とステージング行をまとめて保存する（新規・更新の両方）。
	Save(ctx context.Context, run *IngestionRun) error
	FindByDistributor(ctx context.Context, distributorID shareddomain.ID) ([]*IngestionRun, error)
}
