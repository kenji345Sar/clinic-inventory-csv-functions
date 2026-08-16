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
// 卸商品マスタへ反映する」段を分けている(docs/design.md「全体の流れ」)。
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
	Created     int
	Updated     int
	Message     string
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
	if err := uc.runRepo.Save(ctx, run); err != nil {
		return ImportDistributorCatalogResult{}, err
	}

	result := ImportDistributorCatalogResult{
		Status:      run.Status(),
		TotalRows:   len(run.Rows()),
		InvalidRows: len(run.InvalidRows()),
	}

	// 手順5: 読み取れない行が1行でもあれば反映しない。原因不明のまま一部だけ反映して
	// 業務データを壊さないため（backend側のdomain-rules.md「卸連携CSV基盤」）。
	if result.InvalidRows > 0 {
		run.MarkNeedsReview(fmt.Sprintf("%d行が読み取れませんでした", result.InvalidRows), uc.now())
		if err := uc.runRepo.Save(ctx, run); err != nil {
			return result, err
		}
		result.Status = run.Status()
		result.Message = run.Message()
		return result, nil
	}

	// 手順6: ステージングの内容を卸商品マスタへ反映する。1件でも失敗したら
	// ファイル単位でロールバックし、要確認として残す。
	created, updated, applyErr := 0, 0, error(nil)
	err = uc.transactor.WithinTx(ctx, func(txCtx context.Context) error {
		created, updated, applyErr = uc.apply(txCtx, distributorID, run.ValidRows())
		return applyErr
	})
	if err != nil {
		run.MarkNeedsReview(fmt.Sprintf("反映に失敗しました: %v", err), uc.now())
		if saveErr := uc.runRepo.Save(ctx, run); saveErr != nil {
			return result, saveErr
		}
		result.Status = run.Status()
		result.Message = run.Message()
		return result, nil
	}

	// 手順7: 反映完了を記録する。
	run.MarkApplied(uc.now())
	if err := uc.runRepo.Save(ctx, run); err != nil {
		return result, err
	}
	result.Status = run.Status()
	result.Created = created
	result.Updated = updated
	return result, nil
}

// apply はステージング行を卸商品マスタ・医院別単価へupsertする。
// 突合キーは(卸業者, 卸商品コード)で、既にあれば更新・無ければ新規登録する。
//
// 書き込み先は2テーブル(docs/csv-to-db-flow.md 6章)。
//
//	distributor_products                  … 卸商品マスタ。1商品1行
//	distributor_product_facility_prices   … 医院別単価。医院ごとに単価が違う卸だけ行ができる
//
// テーブル名はgormが構造体から引くためこの層には現れない。実際の名前は
// infrastructure/distributorcatalog/model.go の TableName() を参照。
func (uc *ImportDistributorCatalogUseCase) apply(ctx context.Context, distributorID shareddomain.ID, rows []ingdomain.StagingRow) (created, updated int, err error) {
	for _, row := range rows {
		existing, err := uc.productRepo.FindByDistributorAndCode(ctx, distributorID, row.DistributorProductCode())
		if err != nil {
			return created, updated, err
		}

		var product *distdomain.DistributorProduct
		if existing == nil {
			product, err = distdomain.NewDistributorProduct(distributorID, row.DistributorProductCode(), row.Name(), row.VendorName(), row.UnitPrice())
			if err != nil {
				return created, updated, fmt.Errorf("%d行目: %w", row.RowNo(), err)
			}
			product.SetVendorProductCode(row.VendorProductCode())
			product.SetJANCode(row.JANCode())
			if row.Discontinued() {
				product.Discontinue()
			}
			// distributor_products へINSERT
			if err := uc.productRepo.Create(ctx, product); err != nil {
				return created, updated, fmt.Errorf("%d行目: %w", row.RowNo(), err)
			}
			created++
		} else {
			product = existing
			if err := product.ApplyCatalogUpdate(row.Name(), row.VendorName(), row.VendorProductCode(), row.JANCode(), row.UnitPrice(), row.Discontinued()); err != nil {
				return created, updated, fmt.Errorf("%d行目: %w", row.RowNo(), err)
			}
			// distributor_products をUPDATE（CSVに載る列だけ。取り込み対象外の列は触らない）
			if err := uc.productRepo.Update(ctx, product); err != nil {
				return created, updated, fmt.Errorf("%d行目: %w", row.RowNo(), err)
			}
			updated++
		}

		// 医院別単価。卸側の医院コードをこちらのクリニックIDに突合してから保存する。
		// 商品ごとの単価しか送ってこない卸はここが空になり、下のテーブルには1行も入らない。
		if len(row.FacilityPrices()) == 0 {
			continue
		}
		prices := make([]*distdomain.FacilityPrice, 0, len(row.FacilityPrices()))
		for _, fp := range row.FacilityPrices() {
			facilityID, err := uc.facilities.Resolve(ctx, distributorID, fp.FacilityCode)
			if err != nil {
				return created, updated, fmt.Errorf("%d行目: %w", row.RowNo(), err)
			}
			price, err := distdomain.NewFacilityPrice(product.ID(), facilityID, fp.UnitPrice)
			if err != nil {
				return created, updated, fmt.Errorf("%d行目: %w", row.RowNo(), err)
			}
			prices = append(prices, price)
		}
		// distributor_product_facility_prices へINSERT ... ON CONFLICT DO UPDATE
		// （キーは(卸商品, クリニック)。2回目以降は行が増えず単価が上書きされる）
		if err := uc.facilityPriceRepo.UpsertAll(ctx, prices); err != nil {
			return created, updated, fmt.Errorf("%d行目: %w", row.RowNo(), err)
		}
	}
	return created, updated, nil
}

// finishAsNeedsReview はステージングに1行も入らないまま失敗したケースを記録する。
func (uc *ImportDistributorCatalogUseCase) finishAsNeedsReview(ctx context.Context, distributorID shareddomain.ID, in ImportDistributorCatalogInput, reason string) (ImportDistributorCatalogResult, error) {
	run, err := ingdomain.NewIngestionRun(distributorID, in.S3Key, in.ETag, uc.now())
	if err != nil {
		return ImportDistributorCatalogResult{}, err
	}
	run.MarkNeedsReview(reason, uc.now())
	if err := uc.runRepo.Save(ctx, run); err != nil {
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
