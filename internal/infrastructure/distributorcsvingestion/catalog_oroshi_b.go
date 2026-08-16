package distributorcsvingestion

import (
	"fmt"

	ingapp "clinic-inventory-csv-functions/internal/application/distributorcsvingestion"
)

// 卸B（卸コード: oroshi-b）の商品マスタCSVを中間表現に変換する。
//
//	コード,商品名,医院コード,単価
//	B-1001,犬猫用抗生剤 100mg 100錠,<医院コード>,2480
//
// 医院ごとに扱う商品と単価が違う卸で、**1商品×1医院で1行**になっている。
// 同じ商品コードの行が医院の数だけ並ぶため、最後に商品単位へまとめる。
// 全医院共通の定価は存在しないので、標準単価(UnitPrice)は入れない。
// メーカー名の列が無いため、卸商品の必須項目であるベンダー名は既定値で補う。
const (
	oroshiBEncoding  = encodingUTF8
	oroshiBHasHeader = true
	oroshiBVendor    = "卸B取扱"
)

// 列番号（0始まり）
const (
	oroshiBColCode         = 0
	oroshiBColName         = 1
	oroshiBColFacilityCode = 2
	oroshiBColUnitPrice    = 3
)

func ParseOroshiBCatalogCSV(body []byte) ([]ingapp.CatalogRow, error) {
	records, err := readRecords(body, oroshiBEncoding, oroshiBHasHeader)
	if err != nil {
		return nil, err
	}

	rows := make([]ingapp.CatalogRow, 0, len(records))
	for _, record := range records {
		code := record.Column(oroshiBColCode)
		if code == "" {
			rows = append(rows, invalidRow(record, "卸商品コードが取得できません"))
			continue
		}
		name := record.Column(oroshiBColName)
		if name == "" {
			rows = append(rows, invalidRow(record, "商品名が取得できません"))
			continue
		}
		facilityCode := record.Column(oroshiBColFacilityCode)
		if facilityCode == "" {
			rows = append(rows, invalidRow(record, "医院コードが取得できません"))
			continue
		}
		priceText := record.Column(oroshiBColUnitPrice)
		price, err := parsePrice(priceText)
		if err != nil {
			rows = append(rows, invalidRow(record, fmt.Sprintf("医院別単価を数値として読み取れません(%q)", priceText)))
			continue
		}

		rows = append(rows, ingapp.CatalogRow{
			RowNo:                  record.RowNo,
			Raw:                    record.Raw(),
			DistributorProductCode: code,
			Name:                   name,
			VendorName:             oroshiBVendor,
			FacilityPrices: []ingapp.CatalogFacilityPrice{
				{FacilityCode: facilityCode, UnitPrice: price},
			},
		})
	}

	// 1商品×1医院で1行なので、商品単位にまとめる（5行 → 3件）。
	return groupByProductCode(rows), nil
}
