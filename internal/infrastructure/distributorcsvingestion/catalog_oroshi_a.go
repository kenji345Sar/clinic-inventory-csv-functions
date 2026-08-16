package distributorcsvingestion

import (
	"fmt"

	ingapp "clinic-inventory-csv-functions/internal/application/distributorcsvingestion"
)

// 卸A（卸コード: oroshi-a）の商品マスタCSVを中間表現に変換する。
//
//	コード,商品名,メーカー,単価
//	A-0001,抗生剤 100mg 100錠,メーカーX,1200
//
// 商品ごとの単価を公開する形式だが、**Shift_JIS**で届き、JAN・廃盤の列を持たない。
// サンプル医薬品卸と似ているが、列構成が違うので別のパーサにしている。
const (
	oroshiAEncoding  = encodingShiftJIS
	oroshiAHasHeader = true
)

// 列番号（0始まり）
const (
	oroshiAColCode      = 0
	oroshiAColName      = 1
	oroshiAColVendor    = 2
	oroshiAColUnitPrice = 3
)

func ParseOroshiACatalogCSV(body []byte) ([]ingapp.CatalogRow, error) {
	records, err := readRecords(body, oroshiAEncoding, oroshiAHasHeader)
	if err != nil {
		return nil, err
	}

	rows := make([]ingapp.CatalogRow, 0, len(records))
	for _, record := range records {
		code := record.Column(oroshiAColCode)
		if code == "" {
			rows = append(rows, invalidRow(record, "卸商品コードが取得できません"))
			continue
		}
		name := record.Column(oroshiAColName)
		if name == "" {
			rows = append(rows, invalidRow(record, "商品名が取得できません"))
			continue
		}
		vendorName := record.Column(oroshiAColVendor)
		if vendorName == "" {
			rows = append(rows, invalidRow(record, "ベンダー名が取得できません"))
			continue
		}

		row := ingapp.CatalogRow{
			RowNo:                  record.RowNo,
			Raw:                    record.Raw(),
			DistributorProductCode: code,
			Name:                   name,
			VendorName:             vendorName,
		}
		if priceText := record.Column(oroshiAColUnitPrice); priceText != "" {
			price, err := parsePrice(priceText)
			if err != nil {
				rows = append(rows, invalidRow(record, fmt.Sprintf("単価を数値として読み取れません(%q)", priceText)))
				continue
			}
			row.UnitPrice = &price
		}
		rows = append(rows, row)
	}
	return rows, nil
}
