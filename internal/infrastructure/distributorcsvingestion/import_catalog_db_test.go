package distributorcsvingestion_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	ingapp "clinic-inventory-csv-functions/internal/application/distributorcsvingestion"
	ingdomain "clinic-inventory-csv-functions/internal/domain/distributorcsvingestion"
	shareddomain "clinic-inventory-csv-functions/internal/domain/shared"
	"clinic-inventory-csv-functions/internal/infrastructure/database"
	distinfra "clinic-inventory-csv-functions/internal/infrastructure/distributorcatalog"
	inginfra "clinic-inventory-csv-functions/internal/infrastructure/distributorcsvingestion"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// fakeObjectStore はS3の代わりにメモリ上のCSVを返す。取り込みの検証にS3への到達性を必要としないため。
type fakeObjectStore map[string][]byte

func (s fakeObjectStore) Get(_ context.Context, key string) ([]byte, error) {
	body, ok := s[key]
	if !ok {
		return nil, fmt.Errorf("object not found: %s", key)
	}
	return body, nil
}

// openTestDB はbackendと同じDBに接続する。テーブルはbackend側で作られている前提で、
// このリポジトリからはマイグレーションを行わない。接続できない・未作成の場合はスキップする。
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		dsn = "host=localhost user=apple dbname=clinic_inventory port=5432 sslmode=disable"
	}
	db, err := database.Connect(dsn)
	if err != nil {
		t.Skipf("PostgreSQLに接続できないためスキップします: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil || sqlDB.Ping() != nil {
		t.Skipf("PostgreSQLに接続できないためスキップします: %v", err)
	}
	for _, table := range []string{
		"distributors", "facilities", "distributor_products",
		"distributor_product_facility_prices",
		"distributor_catalog_ingestion_runs", "distributor_catalog_staging_rows",
	} {
		if !db.Migrator().HasTable(table) {
			t.Skipf("テーブル %s がありません。backend(clinic-inventory)を起動してテーブルを作成してください", table)
		}
	}
	return db
}

// setupDistributorAndFacility はテスト用の卸業者とクリニックを用意し、後始末を登録する。
// これらのテーブルはbackendの管理下にあるため、ドメインを介さず直接INSERTする。
func setupDistributorAndFacility(t *testing.T, db *gorm.DB) (shareddomain.ID, shareddomain.ID) {
	t.Helper()

	distributorID := uuid.New()
	corporationID := uuid.New()
	facilityID := uuid.New()

	if err := db.Exec("INSERT INTO distributors (id, name) VALUES (?, ?)", distributorID, "取り込みテスト卸_"+distributorID.String()[:8]).Error; err != nil {
		t.Fatalf("failed to insert distributor: %v", err)
	}
	if err := db.Exec("INSERT INTO corporations (id, name) VALUES (?, ?)", corporationID, "取り込みテスト法人").Error; err != nil {
		t.Fatalf("failed to insert corporation: %v", err)
	}
	if err := db.Exec("INSERT INTO facilities (id, name, facility_type, corporation_id) VALUES (?, ?, ?, ?)",
		facilityID, "取り込みテスト医院", "medical", corporationID).Error; err != nil {
		t.Fatalf("failed to insert facility: %v", err)
	}

	t.Cleanup(func() {
		db.Exec("DELETE FROM distributor_product_facility_prices WHERE facility_id = ?", facilityID)
		db.Exec("DELETE FROM distributor_products WHERE distributor_id = ?", distributorID)
		db.Exec("DELETE FROM distributor_catalog_staging_rows WHERE ingestion_run_id IN (SELECT id FROM distributor_catalog_ingestion_runs WHERE distributor_id = ?)", distributorID)
		db.Exec("DELETE FROM distributor_catalog_ingestion_runs WHERE distributor_id = ?", distributorID)
		db.Exec("DELETE FROM facilities WHERE id = ?", facilityID)
		db.Exec("DELETE FROM corporations WHERE id = ?", corporationID)
		db.Exec("DELETE FROM distributors WHERE id = ?", distributorID)
	})
	return shareddomain.ID(distributorID), shareddomain.ID(facilityID)
}

func newImportUseCase(db *gorm.DB, store fakeObjectStore, mappings map[uuid.UUID]inginfra.ColumnMapping) *ingapp.ImportDistributorCatalogUseCase {
	return ingapp.NewImportDistributorCatalogUseCase(
		store,
		inginfra.NewMappingParserResolver(mappings),
		inginfra.NewFacilityResolver(db),
		database.NewTransactor(db),
		inginfra.NewIngestionRunRepository(db),
		distinfra.NewDistributorProductRepository(db),
		distinfra.NewFacilityPriceRepository(db),
		time.Now,
	)
}

// 商品マスタCSVの取り込み（新規登録→再取り込みでスキップ→内容更新）を、実際のDBに対して通しで確認する。
func TestImportDistributorCatalog(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	distributorID, _ := setupDistributorAndFacility(t, db)
	productRepo := distinfra.NewDistributorProductRepository(db)

	key := fmt.Sprintf("catalogs/%s/master.csv", distributorID)
	store := fakeObjectStore{
		key: []byte("コード,商品名,メーカー,JAN,単価,廃盤\n" +
			"D-0001,抗生剤 100mg,サンプル製薬,4900000000001,\"1,200\",0\n" +
			"D-0002,単価非公表の商品,サンプル製薬,,,0\n"),
	}
	mappings := map[uuid.UUID]inginfra.ColumnMapping{uuid.UUID(distributorID): standardMapping()}
	importCatalog := newImportUseCase(db, store, mappings)

	// 1回目: 2件が新規登録される
	result, err := importCatalog.Execute(ctx, ingapp.ImportDistributorCatalogInput{DistributorID: distributorID, S3Key: key, ETag: "etag-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != ingdomain.StatusApplied {
		t.Fatalf("Status = %s (%s), want applied", result.Status, result.Message)
	}
	if result.Created != 2 || result.Updated != 0 {
		t.Errorf("Created/Updated = %d/%d, want 2/0", result.Created, result.Updated)
	}

	product, err := productRepo.FindByDistributorAndCode(ctx, distributorID, "D-0001")
	if err != nil || product == nil {
		t.Fatalf("D-0001 が登録されていない: %v", err)
	}
	if product.UnitPrice() == nil || *product.UnitPrice() != 1200 {
		t.Errorf("D-0001 UnitPrice = %v, want 1200", product.UnitPrice())
	}
	undisclosed, err := productRepo.FindByDistributorAndCode(ctx, distributorID, "D-0002")
	if err != nil || undisclosed == nil {
		t.Fatalf("D-0002 が登録されていない: %v", err)
	}
	if undisclosed.HasUnitPrice() {
		t.Errorf("D-0002 の単価は非公表(NULL)であるべき: %v", *undisclosed.UnitPrice())
	}

	// 2回目: 同じキー・同じETagなら取り込み済みとしてスキップする
	result, err = importCatalog.Execute(ctx, ingapp.ImportDistributorCatalogInput{DistributorID: distributorID, S3Key: key, ETag: "etag-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Skipped {
		t.Error("同じ内容の再取り込みはスキップされるべき")
	}

	// 3回目: 内容が変わった（ETagが変わった）ら更新される
	store[key] = []byte("コード,商品名,メーカー,JAN,単価,廃盤\n" +
		"D-0001,抗生剤 100mg（改定）,サンプル製薬,4900000000001,1350,0\n" +
		"D-0002,単価非公表の商品,サンプル製薬,,,1\n")
	result, err = importCatalog.Execute(ctx, ingapp.ImportDistributorCatalogInput{DistributorID: distributorID, S3Key: key, ETag: "etag-2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Created != 0 || result.Updated != 2 {
		t.Errorf("Created/Updated = %d/%d, want 0/2", result.Created, result.Updated)
	}
	product, err = productRepo.FindByDistributorAndCode(ctx, distributorID, "D-0001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if product.Name() != "抗生剤 100mg（改定）" || *product.UnitPrice() != 1350 {
		t.Errorf("更新が反映されていない: name=%q price=%v", product.Name(), product.UnitPrice())
	}
	discontinued, err := productRepo.FindByDistributorAndCode(ctx, distributorID, "D-0002")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !discontinued.Discontinued() {
		t.Error("廃盤フラグが反映されていない")
	}
}

// 医院別単価を持つ卸のCSVが、卸商品と医院別単価の両方に反映されることを確認する。
func TestImportDistributorCatalogWithFacilityPrices(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	distributorID, facilityID := setupDistributorAndFacility(t, db)

	key := fmt.Sprintf("catalogs/%s/prices.csv", distributorID)
	store := fakeObjectStore{
		key: []byte("コード,商品名,医院コード,単価\n" +
			fmt.Sprintf("D-1001,医院別単価の商品,%s,880\n", facilityID)),
	}
	mappings := map[uuid.UUID]inginfra.ColumnMapping{
		uuid.UUID(distributorID): {
			HasHeader:         true,
			DefaultVendorName: "サンプル製薬",
			Columns: inginfra.ColumnIndexes{
				DistributorProductCode: col(0),
				Name:                   col(1),
				FacilityCode:           col(2),
				FacilityUnitPrice:      col(3),
			},
		},
	}

	result, err := newImportUseCase(db, store, mappings).Execute(ctx, ingapp.ImportDistributorCatalogInput{DistributorID: distributorID, S3Key: key, ETag: "etag-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != ingdomain.StatusApplied {
		t.Fatalf("Status = %s (%s), want applied", result.Status, result.Message)
	}

	product, err := distinfra.NewDistributorProductRepository(db).FindByDistributorAndCode(ctx, distributorID, "D-1001")
	if err != nil || product == nil {
		t.Fatalf("D-1001 が登録されていない: %v", err)
	}
	// 医院別単価の卸は標準単価を持たない
	if product.HasUnitPrice() {
		t.Errorf("標準単価はNULLであるべき: %v", *product.UnitPrice())
	}
	var price int
	if err := db.Raw("SELECT unit_price FROM distributor_product_facility_prices WHERE distributor_product_id = ? AND facility_id = ?",
		uuid.UUID(product.ID()), uuid.UUID(facilityID)).Scan(&price).Error; err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if price != 880 {
		t.Fatalf("医院別単価が反映されていない: %d", price)
	}
}

// 読み取れない行があるCSVは反映せず「要確認」で止まることを確認する。
func TestImportDistributorCatalogNeedsReview(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	distributorID, _ := setupDistributorAndFacility(t, db)

	key := fmt.Sprintf("catalogs/%s/broken.csv", distributorID)
	store := fakeObjectStore{
		key: []byte("コード,商品名,メーカー,JAN,単価,廃盤\n" +
			"D-9001,正常な行,サンプル製薬,,500,0\n" +
			"D-9002,単価が壊れている行,サンプル製薬,,いくらでも,0\n"),
	}
	mappings := map[uuid.UUID]inginfra.ColumnMapping{uuid.UUID(distributorID): standardMapping()}

	result, err := newImportUseCase(db, store, mappings).Execute(ctx, ingapp.ImportDistributorCatalogInput{DistributorID: distributorID, S3Key: key, ETag: "etag-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != ingdomain.StatusNeedsReview {
		t.Fatalf("Status = %s, want needs_review", result.Status)
	}
	if result.InvalidRows != 1 {
		t.Errorf("InvalidRows = %d, want 1", result.InvalidRows)
	}

	// 正常だった行も含めて、商品マスタには何も反映されていないこと
	product, err := distinfra.NewDistributorProductRepository(db).FindByDistributorAndCode(ctx, distributorID, "D-9001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if product != nil {
		t.Error("要確認で止まった取り込みは、正常行も反映されないべき")
	}

	// 原因を追えるよう、ステージングには原文付きで残っていること
	runs, err := inginfra.NewIngestionRunRepository(db).FindByDistributor(ctx, distributorID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("len(runs) = %d, want 1", len(runs))
	}
	invalid := runs[0].InvalidRows()
	if len(invalid) != 1 {
		t.Fatalf("len(InvalidRows) = %d, want 1", len(invalid))
	}
	if invalid[0].Raw() == "" || invalid[0].ErrorMessage() == "" {
		t.Errorf("原文とエラー理由が残っていない: %+v", invalid[0])
	}
}
