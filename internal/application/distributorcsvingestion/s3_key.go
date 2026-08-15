package distributorcsvingestion

import (
	"fmt"
	"strings"
)

// CatalogKeyPrefix は卸が商品マスタCSVを置くS3キーのプレフィックス。
// 発注CSV(orders/{卸コード}/...)と同じく卸ごとにフォルダを分けるため、
// どの卸のCSVかはCSVの中身ではなく置かれた場所で決まる(docs/design.md「S3キーの規約」)。
const CatalogKeyPrefix = "catalogs/"

// ParseCatalogKey は catalogs/{卸コード}/{ファイル名}.csv から卸コードを取り出す。
// 卸コードはbackend(clinic-inventory)の distributors.code で、
// 卸業者にフォルダを案内する場面で人が読めるようUUIDではなくコードを使っている。
func ParseCatalogKey(key string) (string, error) {
	if !strings.HasPrefix(key, CatalogKeyPrefix) {
		return "", fmt.Errorf("商品マスタCSVのキーではありません: %s", key)
	}
	rest := strings.TrimPrefix(key, CatalogKeyPrefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("キーの形式が %s{卸コード}/{ファイル名} ではありません: %s", CatalogKeyPrefix, key)
	}
	return parts[0], nil
}
