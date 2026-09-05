// cmd/csvsync は卸業者がS3に置いた商品マスタCSVを取り込み、卸商品マスタへ反映する定期実行コマンド。
//
// S3イベント駆動ではなく、プレフィックス配下を一覧するポーリング方式にしている。
// バックエンド・DBはGCP側に置く想定で、AWSのS3イベントを直接受け取れないため
// (docs/catalog-import-pipeline.md「取り込みの起動方法」)。
//
// 処理本体はユースケース層にあり、ここは配線だけ。将来Cloud Functions + Cloud Schedulerに
// 載せ替える際は、同じユースケースを関数エントリポイントから呼ぶ。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	ingapp "clinic-inventory-csv-functions/internal/application/distributorcsvingestion"
	"clinic-inventory-csv-functions/internal/infrastructure/database"
	distinfra "clinic-inventory-csv-functions/internal/infrastructure/distributorcatalog"
	inginfra "clinic-inventory-csv-functions/internal/infrastructure/distributorcsvingestion"
	"clinic-inventory-csv-functions/internal/infrastructure/storage"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	prefix := flag.String("prefix", ingapp.CatalogKeyPrefix, "取り込み対象のS3キープレフィックス")
	flag.Parse()

	ctx := context.Background()

	// 接続先はbackend(clinic-inventory)と同じDB。テーブルの作成・変更はbackend側の責務のため、
	// ここではマイグレーション(AutoMigrate)を一切行わない。既にあるテーブルに読み書きするだけ。
	db, err := database.Connect(env("DATABASE_DSN", "host=localhost user=apple dbname=clinic_inventory port=5432 sslmode=disable"))
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("failed to load AWS config: %v", err)
	}
	s3Client := s3.NewFromConfig(awsCfg)
	bucket := env("S3_BUCKET_CATALOGS", "clinic-inventory-orders-dev")
	reader := storage.NewS3Reader(s3Client, bucket)

	importCatalog := ingapp.NewImportDistributorCatalogUseCase(
		reader,
		inginfra.DefaultParsers(),
		inginfra.NewDistributorResolver(db),
		inginfra.NewFacilityResolver(db),
		database.NewTransactor(db),
		inginfra.NewIngestionRunRepository(db),
		distinfra.NewDistributorProductRepository(db),
		distinfra.NewFacilityPriceRepository(db),
		time.Now,
	)

	objects, err := reader.List(ctx, *prefix)
	if err != nil {
		log.Fatalf("%v", err)
	}
	fmt.Printf("s3://%s/%s: %d objects\n", bucket, *prefix, len(objects))

	needsReview := 0
	for _, obj := range objects {
		// S3ではフォルダ自体がオブジェクトとして見えることがあるため、CSV以外は無視する。
		if !strings.HasSuffix(strings.ToLower(obj.Key), ".csv") {
			continue
		}
		distributorCode, err := ingapp.ParseCatalogKey(obj.Key)
		if err != nil {
			log.Printf("skip %s: %v", obj.Key, err)
			continue
		}
		result, err := importCatalog.Execute(ctx, ingapp.ImportDistributorCatalogInput{
			DistributorCode: distributorCode,
			S3Key:           obj.Key,
			ETag:            obj.ETag,
		})
		if err != nil {
			log.Printf("error %s: %v", obj.Key, err)
			continue
		}
		switch {
		case result.Skipped:
			fmt.Printf("skip(取り込み済み) %s\n", obj.Key)
		case result.Status == "needs_review":
			needsReview++
			fmt.Printf("要確認 %s: %s (全%d行 / 読み取れない行%d)\n", obj.Key, result.Message, result.TotalRows, result.InvalidRows)
		default:
			fmt.Printf("反映 %s: 全%d行 (新規%d / 更新%d)\n", obj.Key, result.TotalRows, result.Created, result.Updated)
		}
	}

	if needsReview > 0 {
		// 自動リトライはしない方針のため、ここでは異常終了させず件数だけ知らせる。
		fmt.Printf("要確認のファイルが%d件あります。distributor_catalog_ingestion_runs を確認してください。\n", needsReview)
	}
}
