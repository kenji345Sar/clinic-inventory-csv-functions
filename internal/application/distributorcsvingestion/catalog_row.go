package distributorcsvingestion

import (
	"context"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// CatalogRow は卸から届いた商品マスタCSVの1行を、卸ごとの形式差を取り除いて
// 正規化した中間表現(docs/design.md「全体の流れ」)。
// 卸別のパーサはこの形に変換するところまでを担当し、以降の反映処理は全卸で共通になる。
type CatalogRow struct {
	RowNo                  int    // CSV上の行番号（1始まり）
	Raw                    string // CSVの原文。突合に失敗した行を人が追えるように保持する
	DistributorProductCode string
	Name                   string
	VendorName             string
	VendorProductCode      string
	JANCode                string
	// UnitPrice は標準単価。nilは「卸が単価を公表していない」を表す（0円と区別する）。
	UnitPrice      *int
	FacilityPrices []CatalogFacilityPrice
	Discontinued   bool
	// ErrorMessage が空でない行は読み取りに失敗した行。パースは途中で止めず、
	// 理由を付けて最後まで読み切る（1行の不備でファイル全体が見えなくなるのを避ける）。
	ErrorMessage string
}

// CatalogFacilityPrice は医院ごとに単価を決めている卸のCSVから読み取った1件。
// FacilityCodeは卸側の医院コードで、こちらのクリニックIDへの変換は反映時に行う。
type CatalogFacilityPrice struct {
	FacilityCode string
	UnitPrice    int
}

// CatalogCsvParser は卸ごとのCSVを中間表現へ変換するポート。
// 実装は「列マッピング定義で動く汎用パーサ」を基本とし、構造が特殊な卸だけ専用実装を足す
// (docs/design.md「卸ごとのフォーマット差」)。
type CatalogCsvParser interface {
	Parse(body []byte) ([]CatalogRow, error)
}

// ParserResolver は卸コードに対応するパーサを返すポート。どの卸のCSVかはS3キーの
// プレフィックス(catalogs/{卸コード}/)から決まるため、卸コードだけで解決できる。
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
