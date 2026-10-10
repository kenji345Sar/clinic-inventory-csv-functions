# 商品マスタCSVがDBに入るまで

「S3に置かれた1本のCSVが、どのコードを通って、どのテーブルに入るのか」を実データで追うノート。
設計の理由は[catalog-import-pipeline.md](catalog-import-pipeline.md)、テーブル定義は clinic-inventory 側の
`docs/architecture/database-schema.md` を参照。

例に使うのは**卸B**（卸コード `oroshi-b`）。医院ごとに扱う商品と単価が違う卸で、いちばん変換が多い。
廃盤フラグの例だけは、廃盤列を持つ**サンプル医薬品卸**（`sample-pharma`）を使う（6章）。

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

ここから先が [ImportDistributorCatalogUseCase.Execute](../internal/application/distributorcsvingestion/import_distributor_catalog.go#L78)
の中身。**コード内の「手順1〜7」のコメントと、この文書の章が対応している。**

```
S3のCSV (5行)
   ▼ 手順1  同じキー・同じETagが反映済みならスキップ
   ▼ 手順2  卸コード → 卸ID
   ▼ 手順3  パーサを引く → ダウンロード → 変換        ← 4章（卸ごと）
中間表現 CatalogRow (3件・医院別単価つき)
   ▼ 手順3(続き)  業務ルール・医院の実在を確かめる    ← 5章（共通）
   ▼ 手順4  ステージングに保存                        ← 5章（共通）
distributor_catalog_staging_rows (3行) ＋ distributor_catalog_ingestion_runs (1行)
   ▼ 手順5  不備のある行があればここで打ち切り
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
| `uc.facilities.ResolveAll` | [FacilityResolver](../internal/infrastructure/distributorcsvingestion/facility_resolver.go#L32) |
| `uc.transactor.WithinTx` | [database.Transactor](../internal/infrastructure/database/database.go) |
| `uc.runRepo` / `uc.productRepo` / `uc.facilityPriceRepo` | infrastructure配下の同名の型 |

卸IDは設定ファイルではなく **`distributors` テーブルを code で引く**（コードとIDの対応は
backend のDBが持ち主のため）。未登録のコードならここで止まり、そのファイルは取り込まれない。

---

## 4. CSV → 中間表現（手順3）＝ 卸ごとに違う唯一の段

分岐点はユースケースのこの1行だけ。

```go
parser, err := uc.parsers.Resolve(in.DistributorCode)   // L95  "oroshi-b" → ParseOroshiBCatalogCSV
rows, err := parser.Parse(body)                         // L103  卸Bの関数が呼ばれる
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
④ import_distributor_catalog.go:95  uc.parsers.Resolve("oroshi-b") → ParseOroshiBCatalogCSV
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

### 1行ごとに何を確かめているか（卸B）

パーサが見ているのは「**値があるか**」と「**数値として読めるか**」だけ。上から順に確かめ、
最初に引っかかった理由で `invalidRow` にして次の行へ進む。

| 列 | 確かめること | 引っかかったときの `ErrorMessage` |
|---|---|---|
| 0 コード | 空でない | 卸商品コードが取得できません |
| 1 商品名 | 空でない | 商品名が取得できません |
| 2 医院コード | 空でない | 医院コードが取得できません |
| 3 単価 | `,` `円` 空白を除いて整数になる | 医院別単価を数値として読み取れません("…") |

この段で**やっていないこと**:

- **文字数のチェックは無い。** 反映先の列はすべて `text` 型（上限なし）で、どの卸のパーサにも長さの判定は無い。
  1,000文字の商品名もそのまま入る
- **0円・マイナスはここでは通る。** `"0"` は数値として読めるため。弾くのは直後の全卸共通のチェック（5章）
- **医院が実在するかは見ない。** これも直後の全卸共通のチェック（5章）
- 同じ商品コードで商品名が行ごとに違っても気づかない。最初の行の商品名が採用される

卸ごとの違い: 卸A・サンプル医薬品卸は「ベンダー名が空でない」も確かめる（卸Bは既定値 `卸B取扱` で埋めるので見ない）。
単価は**空欄を許す**（空欄＝非公表として `UnitPrice` を nil にする）。卸Bは空欄も不備として弾く
（`医院別単価を数値として読み取れません("")`）。

#### 不備のあるCSVだとどうなるか

```csv
コード,商品名,医院コード,単価
B-1001,犬猫用抗生剤 100mg 100錠,578c4442-911c-41e8-bd2a-f39c611288ba,2480
,犬猫用消炎鎮痛剤 50mg 50錠,578c4442-911c-41e8-bd2a-f39c611288ba,1980
B-1003,動物用消毒液 500mL,578c4442-911c-41e8-bd2a-f39c611288ba,880円
B-1001,犬猫用抗生剤 100mg 100錠,494100fc-9a7c-4d4a-a15c-7400a667fca9,要相談
```

| 行 | 結果 |
|---|---|
| 2 | 読めた |
| 3 | **不備**: 卸商品コードが取得できません |
| 4 | 読めた（`880円` → 880） |
| 5 | **不備**: 医院別単価を数値として読み取れません("要相談") |

`groupByProductCode` は不備の行には手を付けずそのまま並べるので、2行目と5行目は同じ `B-1001` でも畳まれない。
この結果は手順4でステージングに4行入り、手順5で止まる（5章）。

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

### 保存する前に業務ルールを確かめる（手順3の続き）

パーサが確かめるのは「値があるか・数値として読めるか」まで。その先の、全卸に共通するルールは
[validateCatalogRows](../internal/application/distributorcsvingestion/validate_catalog_rows.go) が
ステージングに積む前に確かめ、引っかかった行の `ErrorMessage` を埋める。
**パーサで読めなかった行と同じ扱いになる**ので、不備のある行はすべて理由付きでステージングに残り、手順5で止まる。

| 確かめること | ルールの持ち主 | 引っかかったときの `ErrorMessage` |
|---|---|---|
| 商品名・ベンダー名が空でない、標準単価は1円以上（nilは可） | ドメイン [ValidateCatalogValues](../internal/domain/distributorcatalog/distributor_product.go#L60) | 単価は1円以上で指定してください |
| 医院別単価は1円以上 | ドメイン [ValidateFacilityUnitPrice](../internal/domain/distributorcatalog/facility_price.go#L40) | 医院コード 578c4442…: 医院別単価は1円以上で指定してください |
| 医院コードのクリニックが在る | `facilities`（[ResolveAll](../internal/infrastructure/distributorcsvingestion/facility_resolver.go#L32) で1回引く） | 医院コード C0012 に対応するクリニックが見つかりません |
| 同じ商品×同じ医院の単価が2行無い | CSVの中だけで判定 | 医院コード 578c4442… の単価が2行あります |

商品名・ベンダー名はパーサも確かめているので、ここで引っかかるのは単価と医院の3つ。
ルールそのものはドメインの関数に置いてあり、ここはそれを呼ぶだけ。反映（手順6）で使う
`NewDistributorProduct` などの生成処理も同じ関数を呼んでいるので、ルールを変えるときはドメインの1か所で済む。

**手順6まで待たずにここで確かめる理由**: 手順6で見つかると、そのファイルはまるごとロールバックされ、
`message` には最初に見つかった1件の理由しか残らない。不備が3か所あれば「直して置き直す → 次の不備が見つかる」を
3回繰り返すことになる。ここで見つければ、1回の取り込みで全部の不備が行番号付きで分かる。

卸Bのように商品単位にまとめた行では、どのCSV行の医院別単価が不備でも、エラーはその商品が
**最初に現れた行**に付く（5行目の医院コードが不正でも2行目に付く）。どれかが分かるよう、メッセージに医院コードを入れている。

#### 0円と存在しない医院を含むCSVの例

```csv
コード,商品名,医院コード,単価
B-1001,犬猫用抗生剤 100mg 100錠,578c4442-911c-41e8-bd2a-f39c611288ba,2480
B-1002,犬猫用消炎鎮痛剤 50mg 50錠,578c4442-911c-41e8-bd2a-f39c611288ba,0
B-1003,動物用消毒液 500mL,C0012,880
```

3行ともパーサは通る（値はあり、単価も数値として読める）。その後のチェックで2行が不備になる。

| row_no | valid | error_message |
|---|---|---|
| 2 | true | |
| 3 | false | 医院コード 578c4442-…: 医院別単価は1円以上で指定してください |
| 4 | false | 医院コード C0012 に対応するクリニックが見つかりません |

`status = needs_review` / `message = 2行に不備があります` で止まり、商品マスタは触らない。

### ステージングに保存する（手順4）

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

読み取れない行・ルールに合わない行が1行でもあれば、**ここで `needs_review` にして終わる**（＝反映しない）。

4章の不備のあるCSVなら、ステージングはこうなって手順5で止まる。

| row_no | valid | error_message | distributor_product_code | facility_prices |
|---|---|---|---|---|
| 2 | true | | B-1001 | `[{…,"UnitPrice":2480}]` |
| 3 | false | 卸商品コードが取得できません | | |
| 4 | true | | B-1003 | `[{…,"UnitPrice":880}]` |
| 5 | false | 医院別単価を数値として読み取れません("要相談") | | |

`distributor_catalog_ingestion_runs` は `status = needs_review` / `message = 2行に不備があります`。
**読めた2行も反映しない。** 一部だけ反映すると、卸が送ってきた単価の一部だけが新しくなり、
どこまでが新しい値なのか誰にも分からなくなる。

---

## 6. 卸商品マスタへ反映する（手順6）

[apply](../internal/application/distributorcsvingestion/import_distributor_catalog.go#L206) が
**読む → 比べる → 書く**の3段で処理する。全体が**1つのトランザクション**で、1件でも失敗すればファイル単位でロールバックする。

```
読む    distributor_products                 その卸の商品を全件          SELECT 1回
        distributor_product_facility_prices  その卸の医院別単価を全件    SELECT 1回
          ▼  メモリ上の map にする（卸商品コード → 商品 / (商品, 医院) → 単価）
          　 医院コード → クリニックID の map は手順3の続きで作ったものを受け取る
比べる  ステージング行を1件ずつ map と突き合わせる（DBには行かない）
          ・商品が無い          → NewDistributorProduct（IDをここで採番）→ 書き込み対象
          ・有って値が変わった  → UpdateFromDistributorCatalog が changed=true → 書き込み対象
          ・有って値が同じ      → 何もしない
          ・医院別単価も同じく、今の単価と違うもの・無いものだけ書き込み対象
          ▼
書く    distributor_products                 INSERT … ON CONFLICT DO UPDATE  1回
        distributor_product_facility_prices  INSERT … ON CONFLICT DO UPDATE  1回
```

DBとの往復は商品が何件あっても**読み2回＋書き2回**で変わらない（医院を引く1回は手順3の続きで済んでいる）。
書き込みは「新規」と「変更」を分けずに1文で送り、どちらになるかは `(卸ID, 卸商品コード)` の
ユニーク制約に当たるかどうかでDBが決める（既にあればCSVに載る列だけを上書きし、`id` は変えない）。

1文に並べるのは1,000件まで。PostgreSQLは1文のパラメータ数が65,535までなので、
卸商品（1行9列）を7,000件超並べると失敗する。それを超える分は同じトランザクションの中で
複数文に分けて送る（[upsertBatchSize](../internal/infrastructure/distributorcatalog/distributor_product_repository.go#L17)）。

新規商品の `id` は書く前の「比べる」段で採番しているので、医院別単価はDBに問い合わせずに
商品IDを持てる。ただし医院別単価は商品を参照するので、書く順番は商品 → 医院別単価。

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

### DBから何を引いているか

取り込み全体でDBを読むのは5か所。

| 手順 | 引くテーブル | 条件 | 何に使うか |
|---|---|---|---|
| 1 | `distributor_catalog_ingestion_runs` | S3キー＋ETag | 反映済みならスキップ（7章） |
| 2 | `distributors` | 卸コード | 卸IDを得る |
| 3(続き) | `facilities` | CSVに出てくる医院コード | 実在するクリニックか（[ResolveAll](../internal/infrastructure/distributorcsvingestion/facility_resolver.go#L32)）。5章 |
| 6 | `distributor_products` | 卸ID（全件） | 新規か既存か、既存なら値が変わったか（[FindByDistributor](../internal/infrastructure/distributorcatalog/distributor_product_repository.go#L27)） |
| 6 | `distributor_product_facility_prices` | 卸IDの商品（全件） | 医院別単価が変わったか（[FindByDistributor](../internal/infrastructure/distributorcatalog/facility_price_repository.go#L24)） |

商品の「値が変わったか」は [UpdateFromDistributorCatalog](../internal/domain/distributorcatalog/distributor_product.go#L112) が判定する。
CSVに載る列（商品名・ベンダー名・ベンダー商品コード・JAN・単価・廃盤）のどれか1つでも違えば `changed = true`。
単価は「nil（非公表）同士は同じ、nilと値ありは違う」とみなす。

取り込み結果の件数は次の意味になる。

| 件数 | 意味 |
|---|---|
| `Created` | 新規登録した商品 |
| `Updated` | 既にあり、値が変わった商品 |
| `Unchanged` | 既にあり、値が変わらなかった商品（書き込まない） |
| `PriceChanges` | 新規・変更のあった医院別単価（商品の値が同じで単価だけ変わることがあるため別に数える） |

変わった中身を反映前に人が確認する仕組みは未実装（[catalog-import-pipeline.md](catalog-import-pipeline.md)7章）。

### 手順6では業務ルールを確かめ直さない

単価・医院・重複は手順3の続き（5章）で確かめ済みで、手順6に来るのはそれを通った行だけ。
`NewDistributorProduct` / `UpdateFromDistributorCatalog` / `NewFacilityPrice` も同じルールを確かめるが、
それは呼び出し側が確かめ忘れたときに不正な値の集約を作らないための最後の守りで、通常の取り込みでは引っかからない。

手順6が失敗するのは、DBへの書き込み自体が失敗したとき（接続断・制約違反など）。そのときはファイル全体を
ロールバックし、`message = 反映に失敗しました: …` に理由が1つ記録される。

### 2回目の取り込みを例に追う（卸B）

2章のCSVを反映した後に、卸Bから次のCSVが届いたとする。

```csv
コード,商品名,医院コード,単価
B-1001,犬猫用抗生剤 100mg 100錠,578c4442-911c-41e8-bd2a-f39c611288ba,2380
B-1001,犬猫用抗生剤 100mg 100錠,494100fc-9a7c-4d4a-a15c-7400a667fca9,2560
B-1003,動物用消毒液 500mL,578c4442-911c-41e8-bd2a-f39c611288ba,880
B-1004,犬用駆虫薬 10錠,494100fc-9a7c-4d4a-a15c-7400a667fca9,3200
```

前回からの違い: B-1001の医院1が値下げ（2,480 → 2,380）／B-1002が載っていない／
B-1003の医院2が載っていない／B-1004が初登場。

手順3で4行 → 3件（B-1001 / B-1003 / B-1004）になる。

**読む**: 前回の反映結果がそのままメモリに載る。

```
商品の map         B-1001 → P1 / B-1002 → P2 / B-1003 → P3
医院別単価の map   (P1,医院1) → 2480 / (P1,医院2) → 2560 / (P2,医院1) → 1980
                   (P3,医院1) → 880  / (P3,医院2) → 910
医院の map         578c4442… → 医院1 / 494100fc… → 医院2   （手順3の続きで作ったもの）
```

**比べる**: 3件を map と突き合わせる。

| 件 | 商品の map | 商品 | 医院別単価 |
|---|---|---|---|
| B-1001 | P1 が在る | 商品名・単価(NULL)・廃盤とも同じ → **書かない** | 医院1: 2,480 ≠ 2,380 → **書く**／医院2: 2,560 = 2,560 → 書かない |
| B-1003 | P3 が在る | 同じ → **書かない** | 医院1: 880 = 880 → 書かない。**医院2の910は比べる相手が無いので何もしない** |
| B-1004 | 無い | `NewDistributorProduct` でP4を採番 → **書く** | 医院2: map に無い → **書く** |
| B-1002 | **比べられない**（CSVに無いので回ってこない） | 変化なし | 変化なし |

**書く**: 商品は B-1004 の1件、医院別単価は (P1,医院1) と (P4,医院2) の2件を、それぞれ1文で送る。

結果は `Created = 1` / `Updated = 0` / `Unchanged = 2` / `PriceChanges = 2`。
B-1001は値下げがあったが、変わったのは医院別単価だけなので商品としては `Unchanged` に数える。

**`distributor_products`** — 4行

| distributor_product_code | unit_price | discontinued |
|---|---|---|
| B-1001 | NULL | false |
| B-1002 | NULL | false ← CSVに無いが残る |
| B-1003 | NULL | false |
| B-1004 | NULL | false ← 新規 |

**`distributor_product_facility_prices`** — 6行

| 商品 | 医院 | unit_price |
|---|---|---|
| B-1001 | 本院 No.1 | **2,380** |
| B-1001 | 本院 No.2 | 2,560 |
| B-1002 | 本院 No.1 | 1,980 ← CSVに無いが残る |
| B-1003 | 本院 No.1 | 880 |
| B-1003 | 本院 No.2 | 910 ← CSVに無いが残る |
| B-1004 | 本院 No.2 | 3,200 |

**CSVから消えたものは、商品も医院別単価も削除・停止しない。** 載っていないだけで取引終了と
決めつけると、卸の送り漏れ1回で発注できない商品が出る
（扱いを変えるかは[catalog-import-backend.md](catalog-import-backend.md)4章の未決）。

### 単価の反映（パターン別）

どの列に何が入るかはパターンで決まる。パターンは卸ごとのパーサが `CatalogRow` のどこに値を入れるかで決まり、
手順6の側には `if 医院別なら` の分岐は無い（`FacilityPrices` が空なら医院別単価の処理を飛ばすだけ）。

| パターン | 卸 | `distributor_products.unit_price` | `distributor_product_facility_prices` |
|---|---|---|---|
| 商品ごと | 卸A・サンプル医薬品卸 | 前回と違えばCSVの単価で**上書き** | 触らない |
| 商品ごと・単価が空欄 | 同上 | 前回値があれば**NULLで上書き**（前回の値は消える） | 触らない |
| 医院ごと | 卸B | 常に**NULL**（前回もNULLなので変化なし） | (商品, 医院)ごとに比べ、違えばupsert。載っていない医院の行は残る |

「単価が空欄ならNULLで上書き」は、前回1,200円だった商品が今回空欄なら、DBの単価も消えるということ。
空欄を「前回の値を据え置く」とは解釈しない（卸が単価を引っ込めたのか、据え置きなのかを
CSVから区別できないため、届いた内容をそのまま写す）。

発注でどの単価を使うか（医院別単価が無い医院はどうするか等）はbackend側の責務。

### 販売停止（廃盤）の反映

**今の実装は「廃盤列を見る」方式だけ。** 「CSVに載っていない商品を販売停止にする」方式は無い（上の例のB-1002）。

| 卸 | 廃盤の表し方 | 取り込み後の `discontinued` |
|---|---|---|
| サンプル医薬品卸 | 6列目が `1` | `1` なら true、それ以外（`0`・空欄）なら **false** |
| 卸A | 列が無い | 常に **false**（trueだった商品はfalseに書き換わる） |
| 卸B | 列が無い | 常に **false**（同上） |

サンプル医薬品卸のCSVで追う。

```csv
コード,商品名,メーカー,JAN,単価,廃盤
S-0001,抗生剤 100mg 100錠,サンプル製薬,4900000000001,"1,200",1
```

1. パーサ: `record.Column(5) == "1"` → `CatalogRow.Discontinued = true`（[catalog_sample_pharma.go:63](../internal/infrastructure/distributorcsvingestion/catalog_sample_pharma.go#L63)）
2. ステージング: `distributor_catalog_staging_rows.discontinued = true`
3. 反映: 新規なら `NewDistributorProduct` の後に `Discontinue()`、既存なら `UpdateFromDistributorCatalog(…, discontinued=true)`
4. `distributor_products.discontinued = true`（行は消さない。クリニック商品からの参照が残っているため）

次のCSVで同じ商品の廃盤列が `0` になれば、同じ道を通って **false に戻る**。
廃盤は「CSVの最新の値を写す」もので、一度trueにしたら固定、という扱いではない。

この写し方の帰結として、**卸A・卸Bでは廃盤にした商品も次の取り込みで false に戻る**。
列が無い卸は毎回 `Discontinued = false` を渡し、`UpdateFromDistributorCatalog` がtrueとの違いを「変更」と判定して書くため。
DBを直接直して廃盤にしても、その卸のCSVに載っている限り元に戻る。

---

## 7. 完了の記録と2回目以降（手順7）

反映が終わったら `MarkApplied` で `distributor_catalog_ingestion_runs` を更新する
（`status = applied` / `s3_key` / `etag` / `finished_at`）。

同じファイルをもう一度取り込もうとすると、手順1の `IsAlreadyApplied` が**キー＋ETagの一致**で
拾ってスキップする。CSVを置き直せば内容が変わりETagも変わるので再取り込みされ、
`(卸ID, 卸商品コード)` のupsertなので**行は増えず、値が変わった既存行だけが更新される**（6章）。

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
| 1つの卸だけ行のチェックを足す（文字数など） | その卸の `catalog_*.go` に `invalidRow` を返す条件を足す（4章）。引っかかった行はステージングに理由付きで残る |
| 全卸共通の業務ルールを足す | [distributor_product.go](../internal/domain/distributorcatalog/distributor_product.go) の `ValidateCatalogValues`（ステージング前のチェックと生成・更新の両方がこれを呼ぶ）。backendの同じ集約も揃える（5章） |
| 卸の廃盤列を読むようにする | その卸の `catalog_*.go` で `Discontinued` を埋める。共通側は変更不要（6章） |
| CSVに載っていない商品を販売停止にする | 未実装・未決（6章、[catalog-import-backend.md](catalog-import-backend.md)4章） |
| 反映先の列を増やす | backendでテーブル変更 → [model.go](../internal/infrastructure/distributorcatalog/model.go) → repository → [CatalogRow](../internal/application/distributorcsvingestion/catalog_row.go#L6) |
| 取り込み結果・失敗理由を調べる | `distributor_catalog_ingestion_runs` / `_staging_rows`（7章） |
| 同じCSVを取り込み直す | `ingestion_runs` の該当行とステージング行を消してから実行（[catalog-import-pipeline.md](catalog-import-pipeline.md)5章） |
| 実装がどこか分からない | `grep -rn "<卸コード>" --include="*.go" internal/`（4章） |
