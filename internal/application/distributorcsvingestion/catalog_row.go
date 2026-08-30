package distributorcsvingestion

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
