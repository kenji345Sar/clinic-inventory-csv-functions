# 商品マスタCSVがDBに入るまで

「S3に置かれた1本のCSVが、どのコードを通って、どのテーブルに入るのか」を実データで追うノート。
設計の理由は[catalog-import-pipeline.md](catalog-import-pipeline.md)、テーブル定義は clinic-inventory 側の
`docs/architecture/database-schema.md` を参照。

例に使うのは**卸B**（卸コード `oroshi-b`）。医院ごとに扱う商品と単価が違う卸で、いちばん変換が多い。

最終更新: 2026-09-05

---

## 1. 卸ごとに違うのはどこか

境界は**中間表現 `CatalogRow`**。ここより手前が卸ごと、ここから先は全卸まったく同じコードが動く
（なぜ段を分けているかは[catalog-import-pipeline.md](catalog-import-pipeline.md)2章）。

```
CSV ──[卸ごと]──▶ CatalogRow ──[全卸共通]──▶ DB
```

```
internal/infrastructure/distributorcsvingestion/
├── catalog_sample_pharma.go   卸ごと  サンプル医薬品卸の読み方
├── catalog_oroshi_a.go        卸ごと  卸Aの読み方
├── catalog_oroshi_b.go        卸ごと  卸Bの読み方
├── parser_registry.go         卸ごと  卸コード → どのパーサを使うか の対応表
│
├── csv_util.go                   共通  卸別パーサが使う道具（文字コード変換・単価の数値化など）
├── ingestion_run_repository.go   共通  ステージング保存
├── distributor_resolver.go       共通  卸コード → 卸ID
└── facility_resolver.go          共通  医院コード → クリニックID

internal/application/…/import_distributor_catalog.go   共通  手順1〜7の本体
internal/domain/…                                      共通  取り込み実行・反映先の集約
internal/infrastructure/storage, database, distributorcatalog/  共通
```

共通側に `if 卸コード == "oroshi-b"` のような分岐は無い。**卸コードが出てくるのは卸別パーサと
対応表だけ**（新しい卸を足すときに触る場所は8章）。

---

## 2. 材料と起動

### 卸Bが送ってくるCSV

```csv
コード,商品名,医院コード,単価
B-1001,犬猫用抗生剤 100mg 100錠,578c4442-911c-41e8-bd2a-f39c611288ba,2480
B-1002,犬猫用消炎鎮痛剤 50mg 50錠,578c4442-911c-41e8-bd2a-f39c611288ba,1980
B-1003,動物用消毒液 500mL,578c4442-911c-41e8-bd2a-f39c611288ba,880
B-1001,犬猫用抗生剤 100mg 100錠,494100fc-9a7c-4d4a-a15c-7400a667fca9,2560
B-1003,動物用消毒液 500mL,494100fc-9a7c-4d4a-a15c-7400a667fca9,910
```

**1商品×1医院で1行**。同じ `B-1001` が2行あり、医院によって単価が違う（2,480 / 2,560）。
メーカー名・JAN・廃番の列は無い。医院コードは `facilities.id`（[catalog-import-pipeline.md 7章](catalog-import-pipeline.md)の未決事項）。

置き場所は `s3://<bucket>/catalogs/oroshi-b/catalog-facility-prices.csv`。

### 起動

バッチなのでコントローラー層は無く、`func main()` が入口。
[cmd/csvsync/main.go](../cmd/csvsync/main.go) がやるのは3つだけ。

```go
importCatalog := ingapp.NewImportDistributorCatalogUseCase(reader, ...)  // (a) 配線 (L58)
objects, err := reader.List(ctx, *prefix)                                // (b) 対象を一覧 (L70)
for _, obj := range objects {                                            // (c) 1ファイル1回呼ぶ
    distributorCode, _ := ingapp.ParseCatalogKey(obj.Key)                //     キー → 卸コード (L82)
    result, _ := importCatalog.Execute(ctx, ...)
}
```

**ユースケースの単位は「CSV 1ファイルを取り込む」**。複数ファイルを回すのは入口の都合なので
`main` 側にある。将来Cloud Functionsから1件ずつ渡される形になっても、入口だけ差し替えれば済む。

**CSVの中身は一切見ずに卸が決まる。** 判断材料は置かれた場所だけ。

```
catalogs/oroshi-b/catalog-facility-prices.csv
         ↑ ここが卸コード
```

---

## 3. 全体の流れ

ここから先が [ImportDistributorCatalogUseCase.Execute](../internal/application/distributorcsvingestion/import_distributor_catalog.go#L74)
の中身。**コード内の「手順1〜7」のコメントと、この文書の章が対応している。**

```
S3のCSV (5行)
   ▼ 手順1  同じキー・同じETagが反映済みならスキップ
   ▼ 手順2  卸コード → 卸ID
   ▼ 手順3  パーサを引く → ダウンロード → 変換        ← 4章（卸ごと）
中間表現 CatalogRow (3件・医院別単価つき)
   ▼ 手順4  ステージングに保存                        ← 5章（共通）
distributor_catalog_staging_rows (3行) ＋ distributor_catalog_ingestion_runs (1行)
   ▼ 手順5  読み取れない行があればここで打ち切り
   ▼ 手順6  卸商品マスタへ反映（1ファイル＝1トランザクション）
distributor_products (3行) ＋ distributor_product_facility_prices (5行)
   ▼ 手順7  完了を記録
status = applied
```

ユースケースはすべてインターフェース越しに呼ぶため、ソース上は変数名になっている
（`parser.Parse` では実装を検索できない）。実体は起動時に `main` が差し込む。

| ソース上の見え方 | 実際に動く実装 |
|---|---|
| `uc.objectStore.Get` | [S3Reader](../internal/infrastructure/storage/s3_reader.go#L73) |
| `uc.parsers.Resolve` | [ParserRegistry](../internal/infrastructure/distributorcsvingestion/parser_registry.go#L38) |
| `parser.Parse` | 卸別パーサ（[catalog_oroshi_b.go](../internal/infrastructure/distributorcsvingestion/catalog_oroshi_b.go) など） |
| `uc.distributors.Resolve` | [DistributorResolver](../internal/infrastructure/distributorcsvingestion/distributor_resolver.go#L26) |
| `uc.facilities.Resolve` | [FacilityResolver](../internal/infrastructure/distributorcsvingestion/facility_resolver.go#L30) |
| `uc.transactor.WithinTx` | [database.Transactor](../internal/infrastructure/database/database.go) |
| `uc.runRepo` / `uc.productRepo` / `uc.facilityPriceRepo` | infrastructure配下の同名の型 |

卸IDは設定ファイルではなく **`distributors` テーブルを code で引く**（コードとIDの対応は
backend のDBが持ち主のため）。未登録のコードならここで止まり、そのファイルは取り込まれない。

---

## 4. CSV → 中間表現（手順3）＝ 卸ごとに違う唯一の段

分岐点はユースケースのこの1行だけ。

```go
parser, err := uc.parsers.Resolve(in.DistributorCode)   // L91  "oroshi-b" → ParseOroshiBCatalogCSV
rows, err := parser.Parse(body)                         // L99  卸Bの関数が呼ばれる
```

**コード全体で「卸によって呼び先が変わる」のはここだけ**で、インターフェース越しの呼び出しに
なっている。ただしインターフェースが担っているのは*どの関数を呼ぶかを引くこと*だけで、
*CSVの読み方の違いを吸収すること*ではない。読み方は卸ごとに1ファイルに素で書いてあり、
卸Bの挙動を知るには `catalog_oroshi_b.go` を上から下まで読めばよい（列番号を設定値で渡す
汎用パーサ1本にはしていない。理由は[catalog-import-pipeline.md 4章](catalog-import-pipeline.md)）。

ユースケースに `switch 卸コード` と書かず対応表を引いているのは、**卸が増えたときに手順1〜7を
編集させないため**。卸を足してもユースケースは無変更で済む（触る場所は8章）。

### 呼び先はどう決まるか

`Resolve` は**mapを1回引くだけ**。`if 卸コード == …` の分岐は無い。

```go
// parser_registry.go:38
func (r *ParserRegistry) Resolve(distributorCode string) (ingapp.CatalogCsvParser, error) {
    parse, ok := r.parsers[distributorCode]   // ← mapのキーが卸コード
    if !ok {
        return nil, fmt.Errorf("卸コード %s のCSVパーサがありません…", distributorCode)
    }
    return parse, nil
}
```

Goは**関数を値としてmapに入れられる**ので、キー `"oroshi-b"` に `ParseOroshiBCatalogCSV`
という関数そのものを対応させている。その表を作るのが `DefaultParsers()`。

```go
// parser_registry.go:30
func DefaultParsers() *ParserRegistry {
    return NewParserRegistry(map[string]ParseFunc{
        "sample-pharma": ParseSamplePharmaCatalogCSV, // 商品ごとの単価（UTF-8）
        "oroshi-a":      ParseOroshiACatalogCSV,      // 商品ごとの単価（Shift_JIS）
        "oroshi-b":      ParseOroshiBCatalogCSV,      // 医院ごとの単価（1商品×1医院で1行）
    })
}
```

`uc.parsers` にこの表が入るまでが4段。**どの表を使うかは起動時の引数**で決まり、
**その表の中のどれを使うかは実行時の卸コード**で決まる。

```
① cmd/csvsync/main.go:60   inginfra.DefaultParsers()        表を作って返す
② parser_registry.go:30    map[string]ParseFunc{…}          キーと関数の対応はここで決まる
③ import_distributor_catalog.go:21  uc.parsers               コンストラクタの第2引数として保持
④ import_distributor_catalog.go:91  uc.parsers.Resolve("oroshi-b") → ParseOroshiBCatalogCSV
```

実際の値で追うと、S3キー `catalogs/oroshi-b/…` → `ParseCatalogKey` が `"oroshi-b"` を取り出す
→ `Resolve("oroshi-b")` → `ParseOroshiBCatalogCSV` が返る →
[`ParseFunc.Parse`](../internal/infrastructure/distributorcsvingestion/parser_registry.go#L14)
が `f(body)` としてそれを呼ぶ。

テストでは①に別の表を渡す（[import_catalog_db_test.go:96](../internal/infrastructure/distributorcsvingestion/import_catalog_db_test.go#L96)）。
S3にもDBの対応表にも触らずに取り込みを試せるのが、インターフェースにしている実利。

### 手順3から実装を辿る

**IDEの定義ジャンプでは着かない。** `parser.Parse` を辿っても `ParseFunc.Parse` で止まる
（`f` がどの関数かは実行時に決まるため）。辿り方は2つ。

```bash
# 最短。卸コードは対応表と実装の2ファイルにしか出てこない
grep -rn "oroshi-b" --include="*.go" internal/
```

配線から辿る場合は上の①→④を逆にたどる。本番の配線は `main` の1箇所だけ。

| ファイル | 卸 | 文字コード | 単価の持ち方 | 1商品の行数 |
|---|---|---|---|---|
| [catalog_sample_pharma.go](../internal/infrastructure/distributorcsvingestion/catalog_sample_pharma.go) | サンプル医薬品卸 | UTF-8 | 商品ごと | 1行 |
| [catalog_oroshi_a.go](../internal/infrastructure/distributorcsvingestion/catalog_oroshi_a.go) | 卸A | **Shift_JIS** | 商品ごと | 1行 |
| [catalog_oroshi_b.go](../internal/infrastructure/distributorcsvingestion/catalog_oroshi_b.go) | 卸B | UTF-8 | **医院ごと** | **医院数ぶん** |

### 卸別パーサの中身（卸Bの場合）

各パーサは「自分の卸の列構成」を定数で持ち、`csv_util.go` の共通部品を呼ぶだけ。

```go
const (
    oroshiBEncoding  = encodingUTF8
    oroshiBHasHeader = true
    oroshiBVendor    = "卸B取扱"   // メーカー列が無いので既定値で補う
)
const (
    oroshiBColCode = 0; oroshiBColName = 1; oroshiBColFacilityCode = 2; oroshiBColUnitPrice = 3
)
```

1. [readRecords](../internal/infrastructure/distributorcsvingestion/csv_util.go#L30) — 文字コードをUTF-8に揃え（BOM除去）、ヘッダ行と空行を除く【共通部品】
2. その卸の列番号どおりに値を拾う【卸ごと】
3. [parsePrice](../internal/infrastructure/distributorcsvingestion/csv_util.go#L86) — `"1,200"` `"1200円"` を数値にする【共通部品】
4. [groupByProductCode](../internal/infrastructure/distributorcsvingestion/csv_util.go#L96) — 同じ商品コードの行を1件に畳む【共通部品／**呼ぶのは卸Bだけ**】

**読めない行があっても止めない。** 理由を `ErrorMessage` に入れて最後まで読み切る
（1行の不備でファイル全体が見えなくなるのを避けるため）。

### 変換の結果

3の直後はCSVと1対1の5件。4を通して**3件**になる。

| Code | Name | VendorName | UnitPrice | FacilityPrices |
|---|---|---|---|---|
| B-1001 | 犬猫用抗生剤 | 卸B取扱 | nil | [{医院1, 2480}, {医院2, 2560}] |
| B-1002 | 犬猫用消炎鎮痛剤 | 卸B取扱 | nil | [{医院1, 1980}] |
| B-1003 | 動物用消毒液 | 卸B取扱 | nil | [{医院1, 880}, {医院2, 910}] |

- `UnitPrice`（全医院共通の定価）は **nil**。卸Bにそういう単価は存在しない
- 商品の属性（商品名など）は最初に現れた行のものを採用する
- 卸A・サンプル医薬品卸は 4 を呼ばないので5行なら5件のまま。`UnitPrice` に値が入り
  `FacilityPrices` が空、という逆の形になる

**ここから先、卸Bと卸Aの処理は完全に同じ。** 違いは `CatalogRow` の中身だけになる。

---

## 5. ステージングに保存する（手順4・5）

中間表現を `IngestionRun`（取り込み1回分）に詰め、
[IngestionRunRepository.Save](../internal/infrastructure/distributorcsvingestion/ingestion_run_repository.go#L39)
でDBに保存する。**この時点ではまだ商品マスタを一切触っていない。**

`distributor_catalog_staging_rows`:

| row_no | distributor_product_code | unit_price | facility_prices |
|---|---|---|---|
| 2 | B-1001 | NULL | `[{"FacilityCode":"578c4442…","UnitPrice":2480},{"FacilityCode":"494100fc…","UnitPrice":2560}]` |
| 3 | B-1002 | NULL | `[{"FacilityCode":"578c4442…","UnitPrice":1980}]` |
| 4 | B-1003 | NULL | `[{"FacilityCode":"578c4442…","UnitPrice":880},{"FacilityCode":"494100fc…","UnitPrice":910}]` |

医院別単価はJSONのまま持ち、正規化は次の反映段で行う（そうしている理由は[catalog-import-pipeline.md](catalog-import-pipeline.md)3-3）。
CSVの原文（`raw`）も各行に残るので、失敗した行を後から目で追える。`row_no` はCSV上の行番号（ヘッダが1行目）。

読み取れない行が1行でもあれば、**ここで `needs_review` にして終わる**（＝反映しない）。

---

## 6. 卸商品マスタへ反映する（手順6）

[apply](../internal/application/distributorcsvingestion/import_distributor_catalog.go#L174) が
1行ずつ処理する。全体が**1つのトランザクション**で、1件でも失敗すればファイル単位でロールバックする。

1. `(卸ID, 卸商品コード)` で既存商品を探す
2. 無ければ `NewDistributorProduct` → `Create`、有れば `ApplyCatalogUpdate` → `Update`
3. 医院別単価があれば、医院コードを `FacilityResolver.Resolve` で `facilities.id` に変換し `UpsertAll`

`distributor_product_id` はCSVに無い値なので、**商品行を登録・特定してから**紐付ける。
だから商品 → 医院別単価の順になる。

**`distributor_products`** — 3行。`unit_price` は NULL のまま

| distributor_product_code | name | vendor_name | unit_price |
|---|---|---|---|
| B-1001 | 犬猫用抗生剤 100mg 100錠 | 卸B取扱 | NULL |
| B-1002 | 犬猫用消炎鎮痛剤 50mg 50錠 | 卸B取扱 | NULL |
| B-1003 | 動物用消毒液 500mL | 卸B取扱 | NULL |

**`distributor_product_facility_prices`** — 5行（CSVの行数と一致する）

| 商品 | 医院 | unit_price |
|---|---|---|
| B-1001 | 本院 No.1 | 2,480 |
| B-1001 | 本院 No.2 | 2,560 |
| B-1002 | 本院 No.1 | 1,980 |
| B-1003 | 本院 No.1 | 880 |
| B-1003 | 本院 No.2 | 910 |

卸A・サンプル医薬品卸なら、同じコードを通って `unit_price` に値が入り、
`distributor_product_facility_prices` には行ができない。分けているのはCSVの読み方だけで、
**どのテーブルにどう入るかは共通の1本道**。

---

## 7. 完了の記録と2回目以降（手順7）

反映が終わったら `MarkApplied` で `distributor_catalog_ingestion_runs` を更新する
（`status = applied` / `s3_key` / `etag` / `finished_at`）。

同じファイルをもう一度取り込もうとすると、手順1の `IsAlreadyApplied` が**キー＋ETagの一致**で
拾ってスキップする。CSVを置き直せば内容が変わりETagも変わるので再取り込みされ、
`(卸ID, 卸商品コード)` のupsertなので**行は増えず既存行が更新される**。

ETag判定には「以前と同じ内容に戻したCSVは再取り込みされない」という癖がある。
その理由と巻き戻し手順は[catalog-import-pipeline.md](catalog-import-pipeline.md)5章。

失敗したファイルは商品マスタを**1行も更新していない**。原因は
`distributor_catalog_staging_rows` の `error_message` と `raw` を見る。

---

## 8. やりたいこと別・触る場所

| やりたいこと | 触る場所 |
|---|---|
| 新しい卸に対応する | `catalog_<卸コード>.go` を作る ＋ [対応表](../internal/infrastructure/distributorcsvingestion/parser_registry.go#L30)に1行（4章） |
| 卸のCSVの列構成が変わった | その卸の `catalog_*.go` の列番号定数 |
| 文字コードがShift_JISだった | 同ファイルの `encoding` 定数（変換自体は [csv_util.go](../internal/infrastructure/distributorcsvingestion/csv_util.go#L125)） |
| 反映先の列を増やす | backendでテーブル変更 → [model.go](../internal/infrastructure/distributorcatalog/model.go) → repository → [CatalogRow](../internal/application/distributorcsvingestion/catalog_row.go#L6) |
| 取り込み結果・失敗理由を調べる | `distributor_catalog_ingestion_runs` / `_staging_rows`（7章） |
| 同じCSVを取り込み直す | `ingestion_runs` の該当行とステージング行を消してから実行（[catalog-import-pipeline.md](catalog-import-pipeline.md)5章） |
| 実装がどこか分からない | `grep -rn "<卸コード>" --include="*.go" internal/`（4章） |
