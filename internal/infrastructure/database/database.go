package database

import (
	"context"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func Connect(dsn string) (*gorm.DB, error) {
	return gorm.Open(postgres.Open(dsn), &gorm.Config{})
}

// txContextKey はトランザクション中の*gorm.DBをcontextに載せるためのキー。
type txContextKey struct{}

// From はcontextにトランザクションが載っていればそれを、無ければ通常の接続を返す。
// リポジトリはこれを通してDBを取ることで、単体呼び出しでもトランザクション内でも同じコードで動く。
func From(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(txContextKey{}).(*gorm.DB); ok {
		return tx
	}
	return db
}

// Transactor は複数のリポジトリにまたがる更新を1つのトランザクションにまとめる。
// 発注のような単一集約の親子保存はリポジトリ内でトランザクションを張れば足りるが、
// 商品マスタCSVの反映のように「卸商品」と「医院別単価」を同時に更新する処理では
// リポジトリをまたぐため、呼び出し側(ユースケース)から境界を張れるようにしている。
type Transactor struct {
	db *gorm.DB
}

func NewTransactor(db *gorm.DB) *Transactor {
	return &Transactor{db: db}
}

// WithinTx はfnをトランザクション内で実行する。fnがエラーを返すとロールバックする。
func (t *Transactor) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return t.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txContextKey{}, tx))
	})
}
