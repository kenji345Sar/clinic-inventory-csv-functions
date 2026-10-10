package distributorcsvingestion

import (
	"fmt"

	distdomain "clinic-inventory-csv-functions/internal/domain/distributorcatalog"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// validateCatalogRows は卸別パーサが読み取れた行に、業務ルールと医院の実在を確かめて、
// 引っかかった行の ErrorMessage を埋める（手順3の続き。全卸共通）。
//
// 卸別パーサが見ているのは「値があるか・数値として読めるか」まで。ここで見るのはその先で、
//
//	単価が1円以上か               … ドメインのルール（ValidateCatalogValues / ValidateFacilityUnitPrice）
//	医院コードのクリニックが在るか … facilityIDByCode に無ければ不備
//	同じ商品×同じ医院が2行無いか   … どちらの単価が正しいかCSVから決められないため不備
//
// 反映（手順6）で見つけるとファイル全体のロールバックになり、理由も最初の1件しか残らない。
// ここで見つければ読み取れない行と同じ扱いになり、不備のある行がすべて理由付きで
// ステージングに残る（手順5で止まる）。
//
// 卸Bのように商品単位にまとめた行では、どのCSV行の医院別単価が不備でも、エラーは
// その商品が最初に現れた行に付く。どれかを示すため、メッセージに医院コードを入れる。
func validateCatalogRows(rows []CatalogRow, facilityIDByCode map[string]shareddomain.ID) []CatalogRow {
	type priceKey struct{ productCode, facilityCode string }
	seen := make(map[priceKey]bool)

	for i := range rows {
		row := &rows[i]
		if row.ErrorMessage != "" {
			continue
		}
		if err := distdomain.ValidateCatalogValues(row.Name, row.VendorName, row.UnitPrice); err != nil {
			row.ErrorMessage = err.Error()
			continue
		}
		for _, fp := range row.FacilityPrices {
			key := priceKey{row.DistributorProductCode, fp.FacilityCode}
			if seen[key] {
				row.ErrorMessage = fmt.Sprintf("医院コード %s の単価が2行あります", fp.FacilityCode)
				break
			}
			seen[key] = true
			if err := distdomain.ValidateFacilityUnitPrice(fp.UnitPrice); err != nil {
				row.ErrorMessage = fmt.Sprintf("医院コード %s: %v", fp.FacilityCode, err)
				break
			}
			if _, ok := facilityIDByCode[fp.FacilityCode]; !ok {
				row.ErrorMessage = fmt.Sprintf("医院コード %s に対応するクリニックが見つかりません", fp.FacilityCode)
				break
			}
		}
	}
	return rows
}

// facilityCodesOf はCSVに出てくる医院コードを重複なく集める。まとめて1回で変換するため。
func facilityCodesOf(rows []CatalogRow) []string {
	seen := make(map[string]bool)
	codes := make([]string, 0)
	for _, row := range rows {
		for _, fp := range row.FacilityPrices {
			if !seen[fp.FacilityCode] {
				seen[fp.FacilityCode] = true
				codes = append(codes, fp.FacilityCode)
			}
		}
	}
	return codes
}
