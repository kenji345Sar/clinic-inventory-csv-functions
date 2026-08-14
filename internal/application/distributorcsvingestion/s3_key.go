package distributorcsvingestion

import (
	"fmt"
	"strings"

	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"

	"github.com/google/uuid"
)

// CatalogKeyPrefix は卸が商品マスタCSVを置くS3キーのプレフィックス。
// 発注CSV(orders/{distributorId}/...)と同じく卸ごとにフォルダを分けるため、
// どの卸のCSVかはCSVの中身ではなく置かれた場所で決まる
// (docs/design.md「S3キーの規約」)。
const CatalogKeyPrefix = "catalogs/"

// ParseCatalogKey は catalogs/{distributorId}/{ファイル名}.csv から卸IDを取り出す。
func ParseCatalogKey(key string) (shareddomain.ID, error) {
	if !strings.HasPrefix(key, CatalogKeyPrefix) {
		return shareddomain.ID{}, fmt.Errorf("商品マスタCSVのキーではありません: %s", key)
	}
	rest := strings.TrimPrefix(key, CatalogKeyPrefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[1] == "" {
		return shareddomain.ID{}, fmt.Errorf("キーの形式が %s{卸ID}/{ファイル名} ではありません: %s", CatalogKeyPrefix, key)
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		return shareddomain.ID{}, fmt.Errorf("キーに含まれる卸IDがUUIDではありません: %s", key)
	}
	return shareddomain.ID(id), nil
}
