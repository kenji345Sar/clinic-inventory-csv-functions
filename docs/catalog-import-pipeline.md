# 商品マスタCSV取り込みパイプラインの設計

卸業者から届く商品マスタCSVをS3経由で受け取り、clinic-inventory のDBへ反映するまでの、
**このリポジトリが行う処理**の設計。決まっていない点は「未決」と明記する。

- [catalog-import-backend.md](catalog-import-backend.md) … 対になる文書。**反映先**のclinic-inventory(backend)が、受け皿のテーブルとドメインをどう用意しているか
- [s3-storage.md](s3-storage.md) … S3バケット・IAMの設定と運用
- 反映先テーブルの定義と業務ルールは clinic-inventory 側の `docs/architecture/database-schema.md` /
  `docs/architecture/domain-rules.md`「卸連携CSV基盤」

1本のCSVが実際にどう変換されてDBに入るかを順に追いたい場合は[catalog-csv-to-db-flow.md](catalog-csv-to-db-flow.md)。

最終更新: 2026-09-05

---

## 1. S3キーの規約

このバッチが読むのは `catalogs/{卸コード}/{任意のファイル名}.csv`
(バケット全体のキー規約と、フォルダ名に卸コードを使う理由は[s3-storage.md](s3-storage.md)3章)。

卸ごとにフォルダ(プレフィックス)が分かれているため、**どの卸のCSVかは中身ではなく置かれた場所で決まる**。
CSVに卸を識別する列が無くても判別できる。

卸コード(backendの`distributors.code`。例: `oroshi-b`)から卸ID(`distributors.id`)への変換は
DBを引いて行う([distributor_resolver.go](../internal/infrastructure/distributorcsvingestion/distributor_resolver.go))。
未登録のコードのフォルダに置かれたCSVは取り込まない(卸を登録していないのに商品だけ入る状態を防ぐ)。

---

## 2. 全体の流れ(中間表現を挟む二段構成)

CSVを直接商品マスタに書かず、**いったん共通の中間表現に正規化してステージングテーブルに保存し、
それを元にDBへ反映する**。

```
S3 CSV(卸A形式) ┐
S3 CSV(卸B形式) ├→ [パース] → 中間表現CatalogRow → [ステージング保存] → [検証・反映] → distributor_products
S3 CSV(卸C形式) ┘    卸ごとの差はここだけ                                              + distributor_product_facility_prices
```

段を分ける理由:

- **失敗の切り分けができる**。「CSVが読めない(パース失敗)」のか「読めたが反映できない(コード不一致など)」のかが
  別フェーズになり、要確認をどちらの段で止まったかまで含めて記録できる。
- **反映前に差分を確認できる**。新規◯件・単価変更◯件をステージングの内容から出せるため、
  廃番の誤判定のような事故を反映前に止められる。
- **卸が増えても後段が共通**。前段(CSV→中間表現)だけ卸ごとに足せばよい。

中間表現は [internal/application/distributorcsvingestion/catalog_row.go](../internal/application/distributorcsvingestion/catalog_row.go)、
取り込みが外部に要求する操作(ポート)は同じディレクトリの[ports.go](../internal/application/distributorcsvingestion/ports.go)、
ステージング行は [internal/domain/distributorcsvingestion/ingestion_run.go](../internal/domain/distributorcsvingestion/ingestion_run.go)。
ステージングにはCSVの原文(`raw`)も残し、突合に失敗した行を人が追えるようにする。

どのファイルが「卸ごと」で、どこから先が「全卸共通」かは[catalog-csv-to-db-flow.md](catalog-csv-to-db-flow.md)1章に一覧がある。

---

## 3. 単価の3パターン

卸によって単価の出し方が違う。

| 卸のパターン | 中間表現 | 反映先 |
|---|---|---|
| 商品ごとの単価のみ公開 | `UnitPrice`に値 | `distributor_products.unit_price` |
| 医院ごとに単価を決めている | `FacilityPrices`に医院分の行 | `distributor_product_facility_prices` |
| 単価を公表していない | `UnitPrice`が`nil`、`FacilityPrices`が空 | どちらにも入れない(`unit_price`はNULL) |

「0円」と「非公表」を区別するため、単価はポインタ(`*int`)で持つ。
クリニック商品の仕入単価をどう決めるかは clinic-inventory 側の責務。

### 3-1. パターン別に、テーブルがどうなるか

#### (A) 商品ごとの単価だけを送ってくる卸

CSV(`catalogs/{卸コード}/master.csv`):

```csv
コード,商品名,メーカー,JAN,単価,廃盤
D-0001,抗生剤 100mg,サンプル製薬,4900000000001,"1,200",0
D-0002,消炎鎮痛剤 50mg,サンプル製薬,,980,0
```

`distributor_products` … CSV 1行につき1行:

| id | distributor_id | distributor_product_code | name | vendor_name | jan_code | unit_price | discontinued |
|---|---|---|---|---|---|---|---|
| (採番) | (卸コードから解決) | D-0001 | 抗生剤 100mg | サンプル製薬 | 4900000000001 | **1200** | false |
| (採番) | (卸コードから解決) | D-0002 | 消炎鎮痛剤 50mg | サンプル製薬 | NULL | **980** | false |

`distributor_product_facility_prices` … **行は作られない**（医院別単価が無いため）。

- `id`は取り込み時に採番するUUID。CSVには存在しない。
- `distributor_id`はCSVの中身ではなくS3キー(`catalogs/{卸コード}/`)から決まる。
- 単価が空欄・単価列そのものが無い卸の場合、`unit_price`は**NULL**（＝非公表）になる。0とは区別する。

#### (B) 医院ごとに単価を決めている卸

CSV(1商品×1医院で1行になっている):

```csv
コード,商品名,医院コード,単価
D-1001,鎮痛剤 50mg,<医院AのID>,880
D-1001,鎮痛剤 50mg,<医院BのID>,910
D-1002,消毒液 500ml,<医院AのID>,450
```

`distributor_products` … **商品コード単位にまとめられ、CSV 3行 → 2行**:

| id | distributor_product_code | name | unit_price |
|---|---|---|---|
| P1(採番) | D-1001 | 鎮痛剤 50mg | **NULL** |
| P2(採番) | D-1002 | 消毒液 500ml | **NULL** |

`distributor_product_facility_prices` … 医院の数だけ行ができる:

| distributor_product_id | facility_id | unit_price |
|---|---|---|
| P1 | 医院A | 880 |
| P1 | 医院B | 910 |
| P2 | 医院A | 450 |

- 商品側の`unit_price`は**NULL**のまま。この卸にとって「全医院共通の定価」は存在しないため、
  そこに何かを入れると嘘になる。単価は医院別単価テーブルだけが持つ。
- CSVの「医院コード」はこちらの`facilities.id`へ変換してから保存する
  （変換方法は7章の未決事項）。

このCSVがどのコードを通ってこの2つのテーブルに入るかは[catalog-csv-to-db-flow.md](catalog-csv-to-db-flow.md)で実データを追える。

### 3-2. 2回目以降の取り込みでどうなるか

どちらのパターンも**upsert**なので、同じCSVを何度流しても行は増えない。

| テーブル | 突合キー | 既にある場合 |
|---|---|---|
| `distributor_products` | `(distributor_id, distributor_product_code)` | 商品名・ベンダー・JAN・単価・廃盤フラグを上書き |
| `distributor_product_facility_prices` | `(distributor_product_id, facility_id)` | `unit_price`を上書き |

**CSVから消えた行は、商品も医院別単価も削除しない**（理由と廃番の扱いは5章）。(B)の卸で、
ある医院がCSVから消えた場合も医院別単価の行は残る。契約終了を単価CSVの欠落から判断するのは
危険なため（この扱いを変えるかは未決）。

### 3-3. ステージングにはどう入るか

反映の前段で`distributor_catalog_staging_rows`に入る形も、商品単位で1行になる。

| パターン | staging行 | `unit_price`列 | `facility_prices`列(JSON) |
|---|---|---|---|
| (A) | CSV 1行につき1行 | 1200 | 空 |
| (B) | 商品コード単位にまとめて1行 | NULL | `[{"FacilityCode":"...","UnitPrice":880},{"FacilityCode":"...","UnitPrice":910}]` |

医院別単価はステージング上ではJSONのまま持ち、テーブルへの正規化は反映時に行う。
ステージングは「反映前に人が中身を確認するための一時置き場」であり、
ここで正規化まで行うと段を分けた意味が薄れるため。

---

## 4. 卸ごとのフォーマット差の吸収

**卸ごとにパーサを1つ書く**方針。1社分の読み方が1ファイルで完結する。

```
卸AのCSV ─→ ParseOroshiACatalogCSV      ─┐
卸BのCSV ─→ ParseOroshiBCatalogCSV      ─┼→ []CatalogRow（卸に依存しない中間表現）→ 共通の反映
卸CのCSV ─→ ParseSamplePharmaCatalogCSV ─┘
```

卸コードとパーサの対応表は
[parser_registry.go](../internal/infrastructure/distributorcsvingestion/parser_registry.go) の
`DefaultParsers()`。新しい卸を足すときに触る場所は[catalog-csv-to-db-flow.md](catalog-csv-to-db-flow.md)8章。

### なぜ「設定ファイルで列番号を渡す汎用パーサ1本」をやめたか

当初は、卸コードをキーにしたJSONに列番号を書き、汎用パーサ1本で読む方式にしていた。
「卸が増えても設定を足すだけ」を狙ったが、2社目で破綻した。

- 卸によって違うのは列の順番だけではない。**構造が違う**（卸Bは1商品×1医院で1行）。
  設定では表せないので、汎用パーサの中に「医院別単価形式かどうか」の分岐が入り、
  1つの形式の動きを追うのに3か所を行き来する必要が出た
- 形式が増えるたびに分岐が増え、**どの卸のときにどこを通るのかが読めなくなる**
- 「卸ごとに違う」と言いながら実装が1本しかない状態は、インターフェースを挟んでいる
  意味も薄い（多態の余地が無い）

卸ごとにファイルを分けると、コード量は増えるが**1社の読み方が上から下まで一直線に読める**。
文字コード変換・単価の数値化・医院別単価のまとめといった部品は
[csv_util.go](../internal/infrastructure/distributorcsvingestion/csv_util.go) で共有するので、
素直な卸なら1ファイル数十行で済む。

### 卸別パーサが担当する範囲

| 卸ごとに違う（パーサの中） | 全卸で共通（中間表現より後ろ） |
|---|---|
| 文字コード（UTF-8 / Shift_JIS） | ステージングへの保存 |
| ヘッダ行の有無 | `(卸ID, 卸商品コード)` でのupsert |
| 何列目が何か | 医院コード → クリニックIDの突合 |
| 単価の持ち方（商品ごと / 医院ごと / 無し） | 医院別単価の保存 |
| 廃番の表し方 | 失敗時の「要確認」記録 |
| 1商品が何行になるか（まとめる必要があるか） | 冪等性の判定 |

卸ごとの実際の設定値（どの卸がどの文字コードで、単価をどう持つか）は
[catalog-csv-to-db-flow.md](catalog-csv-to-db-flow.md)4章の一覧表。

### 文字コードをどう扱うか

卸ごとに文字コードが違う（UTF-8 / Shift_JIS）。**自動判別はせず、卸との取り決めを
卸別パーサの先頭に定数で書き**、`readRecords`に渡す。判別は短いCSVで外すうえ、
取り決めは一度決まれば変わらないため。

**申告と違う文字コードのCSVが届いた場合は、変換の時点で検出してファイル単位で止める。**
デコーダは変換できない文字をエラーにせず置換文字に差し替えて進むため、素通しすると
商品名が化けたまま取り込みが成功してしまう。

そもそも文字コードを判定できない理由・BOMの正体・食い違いを検出する仕組みは
[character-encoding.md](character-encoding.md)にまとめてある。

---

### 列が無い項目をどう埋めるか

卸によっては列そのものが無い項目がある。中間表現では空を許し、反映時の埋め方を項目ごとに決めている。

| 項目 | 埋め方 |
|---|---|
| ベンダー（メーカー）名 | 卸商品の必須項目のため、パーサ側の設定`defaultVendorName`で補う |
| JANコード | 任意項目。空のまま取り込む（クリニック側のバーコード消費はJANのある商品でのみ効く） |
| ベンダー商品コード | 任意項目。空のまま取り込む |
| 単価 | 空のまま取り込む（`unit_price`はNULL）。3章参照 |

---

## 5. 冪等性と失敗時の扱い

- **同じキー・同じETagは取り込み済みとしてスキップ**する。ETagはS3オブジェクトの内容から決まるため、
  中身が更新されていれば再取り込みされる。判定は`distributor_catalog_ingestion_runs`テーブル。
  - 注意: 判定は「そのキーで**その内容を**取り込んだことがあるか」なので、**以前と同じ内容に戻したCSVは
    再取り込みされない**（ETagが過去の実行と一致するため）。誤った内容を取り込んでしまい、元のCSVに
    戻して復旧したい場合は、該当する`distributor_catalog_ingestion_runs`の行（とステージング行）を
    消してから実行する。DBの値だけが新しいまま残るのを防ぐため、この癖は把握しておく。
- **反映はupsert**。突合キーは`(distributor_id, distributor_product_code)`。何度流しても結果が同じになる。
- **CSVに無い商品は削除しない**。送付漏れで既存マスタが消えるのを防ぐ。廃番は`discontinued`フラグで表現し、
  CSVに廃番列がある場合のみ反映する。
- **パースは行単位で継続、反映はファイル単位のトランザクション**。1行の不備で全体が見えなくなるのを避けつつ、
  1件でもエラーがあればロールバックして要確認で止める（中途半端な更新を残さない）。
- **文字コードの食い違いはファイル単位で止める**（4章「文字コードをどう扱うか」）。
- **失敗時は自動リトライしない**。要確認として記録し、生データはS3に残して人手対応に委ねる。

### 取り込み結果の見方

1回分の記録は`distributor_catalog_ingestion_runs`、正規化後の行は`distributor_catalog_staging_rows`に残る
（テーブル定義はclinic-inventory側の`docs/architecture/database-schema.md`）。

| `status` | 意味 |
|---|---|
| `applied` | 反映済み |
| `staged` | 中間表現までは作られたが反映前 |
| `needs_review` | 要確認。反映されていない。`message`に理由、ステージング行に原文とエラー内容が残る |

要確認になったものは自動リトライしないため、原因を直したうえでCSVを置き直す（ETagが変わるので再取り込みされる）。

---

## 6. 取り込みの起動方法

S3イベント駆動にはしない。バッチ・DBはGCP側に置く想定で、AWSのS3イベントを直接受け取れないため、
プレフィックス配下を一覧するポーリング方式にしている。

- 現在: `./run.sh`（`cmd/csvsync`）をcron等から実行
- 将来: 同じユースケースをCloud Functionsに載せ、Cloud Schedulerから定刻起動

処理本体はユースケース層にあり、`cmd/csvsync`は配線だけなので、関数エントリポイントを足せば載せ替えられる。
実行コマンドと出力の見方は[README](../README.md)。

---

## 7. 未決

| 項目 | 現状 |
|---|---|
| IAM権限 | 付与済み(2026-08-14)。`catalogs/`配下への`s3:ListBucket`と`s3:GetObject`。ポリシーは[s3-storage.md](s3-storage.md) 3-1章 |
| 卸側の医院コード | 現状は「医院コード = クリニックID(UUID)」として扱っている。実際の卸は自社の医院コード体系を使うため、`(卸業者, 卸側医院コード) → クリニックID`の対応表が必要。変換は[facility_resolver.go](../internal/infrastructure/distributorcsvingestion/facility_resolver.go)1か所に閉じてある |
| 卸側の書き込み手段 | 卸に`catalogs/{卸コード}/`へのPut権限を渡すか、こちらが受領してアップロードするか。当面は後者 |
| 処理済みCSVの退避 | 実施しない（ETagで取り込み済みを判定する）。`processed/`へ移す運用にする場合は`s3:DeleteObject`の追加が必要 |
| 反映前の差分確認 | 未実装。ステージングに中間表現が残っているため、後から足せる |
| 受注確定CSV・売上明細CSV | 未着手 |
