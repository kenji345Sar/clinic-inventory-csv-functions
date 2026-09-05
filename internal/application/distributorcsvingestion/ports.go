package distributorcsvingestion

import (
	"context"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// このファイルは取り込みユースケースが外部に要求する操作(ポート)の宣言だけを置く。
// 実装はinfrastructure配下にあり、起動時にcmd/csvsyncが差し込む。
// 中間表現そのものはcatalog_row.goを参照。

// CatalogCsvParser は卸ごとのCSVを中間表現へ変換するポート。
// 実装は卸ごとに1ファイル(infrastructure/…/catalog_<卸コード>.go)を素直に書く。
// 列マッピング定義で動く汎用パーサ1本にはしない(docs/catalog-import-pipeline.md「卸ごとのフォーマット差」)。
type CatalogCsvParser interface {
	Parse(body []byte) ([]CatalogRow, error)
}

// ParserResolver は卸コードに対応するパーサを返すポート。どの卸のCSVかはS3キーの
// プレフィックス(catalogs/{卸コード}/)から決まるため、卸コードだけで解決できる。
//
// ここがコード中で唯一「卸によって呼び先が変わる」場所。ユースケースに
// switch 卸コード を書くとapplication層がinfrastructureの卸別パーサをimportすることになり
// 依存の向きが逆転するため、対応表はinfrastructure側に置いてこのポート越しに引く。
type ParserResolver interface {
	Resolve(distributorCode string) (CatalogCsvParser, error)
}

// DistributorResolver は卸コードを卸ID(distributors.id)に変換するポート。
// コードとIDの対応はbackendのDBが持ち主のため、設定ファイルには持たずDBを引く。
type DistributorResolver interface {
	Resolve(ctx context.Context, distributorCode string) (shareddomain.ID, error)
}

// ObjectStore はCSV本体を取得するポート（実装はS3）。
type ObjectStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
}

// FacilityResolver は卸側の医院コードをこちらのクリニックIDに変換するポート。
type FacilityResolver interface {
	Resolve(ctx context.Context, distributorID shareddomain.ID, facilityCode string) (shareddomain.ID, error)
}

// Transactor は複数リポジトリにまたがる反映を1つのトランザクションにまとめるポート。
type Transactor interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
