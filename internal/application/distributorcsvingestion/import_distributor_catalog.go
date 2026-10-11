package distributorcsvingestion

import (
	"context"
	"fmt"
	"time"

	distdomain "clinic-inventory-csv-functions/internal/domain/distributorcatalog"
	ingdomain "clinic-inventory-csv-functions/internal/domain/distributorcsvingestion"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
)

// ImportDistributorCatalogUseCase はS3上の商品マスタCSVを1件取り込む。
//
// 「CSVを中間表現に正規化してステージングに保存する」段と「ステージングの内容を
// 卸商品マスタへ反映する」段を分けている(docs/catalog-import-pipeline.md「全体の流れ」)。
// 定期実行の入口(cmd/ingest)・将来のCloud Functionsのどちらからでも呼べるよう、
// 処理の本体はここに置きmain側は配線だけにする。
type ImportDistributorCatalogUseCase struct {
	objectStore       ObjectStore
	parsers           ParserResolver
	distributors      DistributorResolver
	facilities        FacilityResolver
	transactor        Transactor
	runRepo           ingdomain.IngestionRunRepository
	productRepo       distdomain.DistributorProductRepository
	facilityPriceRepo distdomain.FacilityPriceRepository
	now               func() time.Time
}

func NewImportDistributorCatalogUseCase(
	objectStore ObjectStore,
	parsers ParserResolver,
	distributors DistributorResolver,
	facilities FacilityResolver,
	transactor Transactor,
	runRepo ingdomain.IngestionRunRepository,
	productRepo distdomain.DistributorProductRepository,
	facilityPriceRepo distdomain.FacilityPriceRepository,
	now func() time.Time,
) *ImportDistributorCatalogUseCase {
	return &ImportDistributorCatalogUseCase{
		objectStore:       objectStore,
		parsers:           parsers,
		distributors:      distributors,
		facilities:        facilities,
		transactor:        transactor,
		runRepo:           runRepo,
		productRepo:       productRepo,
		facilityPriceRepo: facilityPriceRepo,
		now:               now,
	}
}

type ImportDistributorCatalogInput struct {
	// DistributorCode はS3キー(catalogs/{卸コード}/...)から取り出した卸コード。
	// 対応する卸ID(distributors.id)はDBを引いて解決する。
	DistributorCode string
	S3Key           string
	ETag            string
}

// ImportDistributorCatalogResult は取り込み1件の結果。定期実行のログ・運用確認に使う。
type ImportDistributorCatalogResult struct {
	Skipped     bool // 取り込み済み（同じキー・同じ内容）でスキップした
	Status      ingdomain.Status
	TotalRows   int
	InvalidRows int
	Created     int // 新規登録した商品数
	Updated     int // 既にあり、値が変わった商品数
	Unchanged   int // 既にあり、値が変わらなかった商品数（書き込まない）
	// PriceChanges は新規・変更のあった医院別単価の件数。商品の値が変わらず
	// 単価だけ変わることがあるため、商品の件数とは別に数える。
	PriceChanges int
	Message      string
}

func (uc *ImportDistributorCatalogUseCase) Execute(ctx context.Context, in ImportDistributorCatalogInput) (ImportDistributorCatalogResult, error) {
	// 手順1: 同じキー・同じ内容が反映済みなら何もしない（定期実行での二重取り込みを防ぐ）。
	applied, err := uc.runRepo.IsAlreadyApplied(ctx, in.S3Key, in.ETag)
	if err != nil {
		return ImportDistributorCatalogResult{}, err
	}
	if applied {
		return ImportDistributorCatalogResult{Skipped: true, Status: ingdomain.StatusApplied}, nil
	}

	// 手順2: 卸コードから卸IDを解決する。未登録のコードのフォルダに置かれたCSVは取り込まない。
	distributorID, err := uc.distributors.Resolve(ctx, in.DistributorCode)
	if err != nil {
		return ImportDistributorCatalogResult{}, err
	}

	// 手順3: CSV本体を取得し、その卸のパーサで中間表現に変換する。
	parser, err := uc.parsers.Resolve(in.DistributorCode)
	if err != nil {
		return ImportDistributorCatalogResult{}, err
	}
	body, err := uc.objectStore.Get(ctx, in.S3Key)
	if err != nil {
		return ImportDistributorCatalogResult{}, err
	}
	rows, err := parser.Parse(body)
	if err != nil {
		// ファイルとして読めない（文字コード不正・CSVとして壊れている等）。
		// 行単位の不備ではないため、要確認として記録して終わる。
		return uc.finishAsNeedsReview(ctx, distributorID, in, fmt.Sprintf("CSVを読み取れませんでした: %v", err))
	}

	// 手順3(続き): 業務ルール（単価が1円以上か）と医院の実在を、ステージングに積む前に確かめる。
	// 引っかかった行は読み取れない行と同じく理由付きで残り、手順5で止まる（validate_catalog_rows.go）。
	facilityIDByCode, err := uc.facilities.ResolveAll(ctx, distributorID, facilityCodesOf(rows))
	if err != nil {
		return ImportDistributorCatalogResult{}, err
	}
	rows = validateCatalogRows(rows, facilityIDByCode)

	// 手順4: 中間表現をステージングに保存する（この時点ではまだ商品マスタは更新しない）。
	// 書き込み先は distributor_catalog_ingestion_runs（取り込み1回分）と
	// distributor_catalog_staging_rows（CSV1行分。原文とエラー理由も持つ）。
	run, err := ingdomain.NewIngestionRun(distributorID, in.S3Key, in.ETag, uc.now())
	if err != nil {
		return ImportDistributorCatalogResult{}, err
	}
	for _, row := range rows {
		if row.ErrorMessage != "" {
			run.AddInvalidRow(row.RowNo, row.Raw, row.ErrorMessage)
			continue
		}
		run.AddRow(
			row.RowNo, row.Raw,
			row.DistributorProductCode, row.Name, row.VendorName, row.VendorProductCode, row.JANCode,
			row.UnitPrice, toStagingFacilityPrices(row.FacilityPrices), row.Discontinued,
		)
	}
	if err := uc.runRepo.Create(ctx, run); err != nil {
		return ImportDistributorCatalogResult{}, err
	}

	result := ImportDistributorCatalogResult{
		Status:      run.Status(),
		TotalRows:   len(run.Rows()),
		InvalidRows: len(run.InvalidRows()),
	}

	// 手順5: 読み取れない行・業務ルールに合わない行が1行でもあれば反映しない。原因不明のまま一部だけ反映して
	// 業務データを壊さないため（backend側のdomain-rules.md「卸連携CSV基盤」）。
	if result.InvalidRows > 0 {
		run.MarkNeedsReview(fmt.Sprintf("%d行に不備があります", result.InvalidRows), uc.now())
		if err := uc.runRepo.UpdateStatus(ctx, run); err != nil {
			return result, err
		}
		result.Status = run.Status()
		result.Message = run.Message()
		return result, nil
	}

	// 手順6: ステージングの内容を卸商品マスタへ反映する。1件でも失敗したら
	// ファイル単位でロールバックし、要確認として残す。
	var counts applyCounts
	err = uc.transactor.WithinTx(ctx, func(txCtx context.Context) error {
		var applyErr error
		counts, applyErr = uc.apply(txCtx, distributorID, run.ValidRows(), facilityIDByCode)
		return applyErr
	})
	if err != nil {
		run.MarkNeedsReview(fmt.Sprintf("反映に失敗しました: %v", err), uc.now())
		if saveErr := uc.runRepo.UpdateStatus(ctx, run); saveErr != nil {
			return result, saveErr
		}
		result.Status = run.Status()
		result.Message = run.Message()
		return result, nil
	}

	// 手順7: 反映完了を記録する。
	run.MarkApplied(uc.now())
	if err := uc.runRepo.UpdateStatus(ctx, run); err != nil {
		return result, err
	}
	result.Status = run.Status()
	result.Created = counts.created
	result.Updated = counts.updated
	result.Unchanged = counts.unchanged
	result.PriceChanges = counts.priceChanges
	return result, nil
}

type applyCounts struct {
	created, updated, unchanged, priceChanges int
}

// facilityPriceKey は医院別単価の同一性（卸商品, クリニック）。
type facilityPriceKey struct {
	productID  shareddomain.ID
	facilityID shareddomain.ID
}

// apply はステージング行を卸商品マスタ・医院別単価へ反映する。
//
// 「読む → 比べる → 書く」の3段で、DBとの往復を行数に比例させない。
//
//	読む   … その卸の既存商品・既存の医院別単価を、それぞれ1回で引く
//	         （医院コードの対応は手順3の続きで引いたものを受け取る）
//	比べる … メモリ上でCSVの各行と突き合わせ、新規・変更のあったものだけを書き込み対象にする
//	書く   … 商品、医院別単価の順に、テーブルごとにまとめて INSERT … ON CONFLICT DO UPDATE
//
// 業務ルール・医院の実在・重複は手順3の続きで確かめ済みで、ここに来るのはそれを通った行だけ。
// ドメインの生成・更新もルールを確かめるが、それは呼び出し側が確かめ忘れたときの最後の守り。
//
// 書き込み先は2テーブル(docs/catalog-csv-to-db-flow.md 6章)。
//
//	distributor_products                  … 卸商品マスタ。1商品1行
//	distributor_product_facility_prices   … 医院別単価。医院ごとに単価が違う卸だけ行ができる
//
// テーブル名はgormが構造体から引くためこの層には現れない。実際の名前は
// infrastructure/distributorcatalog/model.go の TableName() を参照。
func (uc *ImportDistributorCatalogUseCase) apply(ctx context.Context, distributorID shareddomain.ID, rows []ingdomain.StagingRow, facilityIDByCode map[string]shareddomain.ID) (applyCounts, error) {
	var counts applyCounts

	// 読む
	existingProducts, err := uc.productRepo.FindByDistributor(ctx, distributorID)
	if err != nil {
		return counts, err
	}
	productByCode := make(map[string]*distdomain.DistributorProduct, len(existingProducts))
	for _, p := range existingProducts {
		productByCode[p.DistributorProductCode()] = p
	}
	existingPrices, err := uc.facilityPriceRepo.FindByDistributor(ctx, distributorID)
	if err != nil {
		return counts, err
	}
	currentPrice := make(map[facilityPriceKey]int, len(existingPrices))
	for _, p := range existingPrices {
		currentPrice[facilityPriceKey{p.DistributorProductID(), p.FacilityID()}] = p.UnitPrice()
	}

	// 比べる
	productsToSave := make([]*distdomain.DistributorProduct, 0)
	queuedProducts := make(map[string]bool) // 書き込み対象に積んだ卸商品コード（同じコードが2行あっても1回だけ積む）
	pricesToSave := make([]*distdomain.FacilityPrice, 0)

	for _, row := range rows {
		code := row.DistributorProductCode()
		product, ok := productByCode[code]
		if !ok {
			product, err = distdomain.NewDistributorProduct(distributorID, code, row.Name(), row.VendorName(), row.UnitPrice())
			if err != nil {
				return counts, fmt.Errorf("%d行目: %w", row.RowNo(), err)
			}
			product.SetVendorProductCode(row.VendorProductCode())
			product.SetJANCode(row.JANCode())
			if row.Discontinued() {
				product.Discontinue()
			}
			// 同じコードが後の行にもう一度出たら、既存商品として扱う
			productByCode[code] = product
			queuedProducts[code] = true
			productsToSave = append(productsToSave, product)
			counts.created++
		} else {
			changed, err := product.UpdateFromDistributorCatalog(row.Name(), row.VendorName(), row.VendorProductCode(), row.JANCode(), row.UnitPrice(), row.Discontinued())
			if err != nil {
				return counts, fmt.Errorf("%d行目: %w", row.RowNo(), err)
			}
			switch {
			case queuedProducts[code]:
				// このCSVの前の行で既に積んである。積み直さず、上の上書きで後の行の値が勝つ
			case changed:
				queuedProducts[code] = true
				productsToSave = append(productsToSave, product)
				counts.updated++
			default:
				counts.unchanged++
			}
		}

		// 医院別単価。商品ごとの単価しか送ってこない卸はここが空になり、1件も積まれない。
		for _, fp := range row.FacilityPrices() {
			facilityID, ok := facilityIDByCode[fp.FacilityCode]
			if !ok {
				return counts, fmt.Errorf("%d行目: 医院コード %s に対応するクリニックが見つかりません", row.RowNo(), fp.FacilityCode)
			}
			key := facilityPriceKey{product.ID(), facilityID}
			price, err := distdomain.NewFacilityPrice(product.ID(), facilityID, fp.UnitPrice)
			if err != nil {
				return counts, fmt.Errorf("%d行目: %w", row.RowNo(), err)
			}
			if current, ok := currentPrice[key]; ok && current == fp.UnitPrice {
				continue
			}
			pricesToSave = append(pricesToSave, price)
			counts.priceChanges++
		}
	}

	// 書く。医院別単価は商品IDを参照するため、商品を先に書く。
	if err := uc.productRepo.UpsertAll(ctx, productsToSave); err != nil {
		return counts, err
	}
	if err := uc.facilityPriceRepo.UpsertAll(ctx, pricesToSave); err != nil {
		return counts, err
	}
	return counts, nil
}

// finishAsNeedsReview はステージングに1行も入らないまま失敗したケースを記録する。
func (uc *ImportDistributorCatalogUseCase) finishAsNeedsReview(ctx context.Context, distributorID shareddomain.ID, in ImportDistributorCatalogInput, reason string) (ImportDistributorCatalogResult, error) {
	run, err := ingdomain.NewIngestionRun(distributorID, in.S3Key, in.ETag, uc.now())
	if err != nil {
		return ImportDistributorCatalogResult{}, err
	}
	run.MarkNeedsReview(reason, uc.now())
	if err := uc.runRepo.Create(ctx, run); err != nil {
		return ImportDistributorCatalogResult{}, err
	}
	return ImportDistributorCatalogResult{Status: run.Status(), Message: reason}, nil
}

func toStagingFacilityPrices(prices []CatalogFacilityPrice) []ingdomain.StagingFacilityPrice {
	if len(prices) == 0 {
		return nil
	}
	converted := make([]ingdomain.StagingFacilityPrice, 0, len(prices))
	for _, p := range prices {
		converted = append(converted, ingdomain.StagingFacilityPrice{FacilityCode: p.FacilityCode, UnitPrice: p.UnitPrice})
	}
	return converted
}
