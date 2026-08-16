package distributorcsvingestion

import (
	"fmt"

	ingapp "clinic-inventory-csv-functions/internal/application/distributorcsvingestion"
)

// サンプル医薬品卸（卸コード: sample-pharma）の商品マスタCSVを中間表現に変換する。
//
//	コード,商品名,メーカー,JAN,単価,廃盤
//	S-0001,抗生剤 100mg 100錠,サンプル製薬,4900000000001,"1,200",0
//
// 商品ごとの単価だけを公開する、もっとも素直な形式。1商品で1行。
// 単価列が空欄なら「単価が無い」として扱い、標準単価は入れない（0とは区別する）。
const (
	samplePharmaEncoding    = encodingUTF8
	samplePharmaHasHeader   = true
	samplePharmaDiscontinue = "1" // 廃盤列がこの値なら廃盤
)

// 列番号（0始まり）
const (
	samplePharmaColCode         = 0
	samplePharmaColName         = 1
	samplePharmaColVendor       = 2
	samplePharmaColJAN          = 3
	samplePharmaColUnitPrice    = 4
	samplePharmaColDiscontinued = 5
)

func ParseSamplePharmaCatalogCSV(body []byte) ([]ingapp.CatalogRow, error) {
	records, err := readRecords(body, samplePharmaEncoding, samplePharmaHasHeader)
	if err != nil {
		return nil, err
	}

	rows := make([]ingapp.CatalogRow, 0, len(records))
	for _, record := range records {
		code := record.Column(samplePharmaColCode)
		if code == "" {
			rows = append(rows, invalidRow(record, "卸商品コードが取得できません"))
			continue
		}
		name := record.Column(samplePharmaColName)
		if name == "" {
			rows = append(rows, invalidRow(record, "商品名が取得できません"))
			continue
		}
		vendorName := record.Column(samplePharmaColVendor)
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
			JANCode:                record.Column(samplePharmaColJAN),
			Discontinued:           record.Column(samplePharmaColDiscontinued) == samplePharmaDiscontinue,
		}

		// 単価は空欄を許す（この卸が値を出していない商品）。その場合は入れない。
		if priceText := record.Column(samplePharmaColUnitPrice); priceText != "" {
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
