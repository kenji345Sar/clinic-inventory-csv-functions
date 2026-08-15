# 手動確認用のサンプルCSV

卸から届くCSVを模したもの。S3にアップロードして取り込みを手動で確認するために使う。

| ファイル | 想定する卸 | 形式 |
|---|---|---|
| `catalog-standard.csv` | 商品ごとの単価を公開する卸 | 商品コード・商品名・メーカー・JAN・単価・廃盤 |
| `catalog-facility-prices.csv` | 医院ごとに扱う商品と単価が違う卸 | 商品コード・商品名・医院コード・単価（1商品×1医院で1行） |

## 医院コードについて

`catalog-facility-prices.csv` の医院コード列には、**ローカルDBの`facilities.id`(UUID)** を入れてある。
現状は「医院コード = クリニックID」として突合しているため（docs/design.md 7章の未決事項）。
実際の卸は自社の医院コードを使うので、対応表を入れる段階でこのサンプルも書き換える。

## ローカルDBの対応表（UUID → 名前）

CSVやS3のキーに出てくるUUIDが何を指すかの早見表。**ローカル開発環境の値**なので、
別の環境で試す場合は自分のDBのIDに差し替える（末尾のSQLで確認できる）。

### 卸業者

S3のフォルダ名には卸コードを使うのでUUIDは出てこないが、DBを直接見るときのために載せる。

| 卸コード | 名前 | id |
|---|---|---|
| **oroshi-b** | **卸B** ← サンプルCSVで使用 | `9aaeb4e9-c369-4850-9cf6-8142ce91d7ea` |
| oroshi-a | 卸A | `be6714bc-6b28-44cc-8549-6c113d702df7` |
| sample-pharma | サンプル医薬品卸 | `df7a547f-2765-40aa-be67-e9f74241e6a3` |
| api-distributor | API経由卸 | `d22de577-268b-4813-90ed-90a1853ec0b1` |
| test-distributor | テスト卸 | `77e19d39-12f9-4cd1-a953-00dadf443c84` |

### クリニック（医院コード列に入れる値）

| 名前 | 法人 | id |
|---|---|---|
| **サンプル動物病院 本院 No.1** ← サンプルCSVで使用 | サンプル動物病院グループ | `578c4442-911c-41e8-bd2a-f39c611288ba` |
| **サンプル動物病院 本院 No.2** ← サンプルCSVで使用 | サンプル動物病院グループ | `494100fc-9a7c-4d4a-a15c-7400a667fca9` |
| サンプル動物病院 本院 No.3（空） | サンプル動物病院グループ | `ee38de0e-c173-4a5e-82c8-4d1854613aef` |
| サンプル動物病院 本院 No.4（空） | サンプル動物病院グループ | `dfd0e236-86a2-4acb-bb1c-e1297313125e` |
| API経由クリニック | API経由の法人 | `38057321-88d6-4a6c-8820-0da36fdd3766` |
| テストクリニック | テスト法人 | `a5111a4f-588a-4f78-b7fb-4360ed1c18a0` |
| テスト医科クリニック | テスト医療法人 | `2808c61b-8c2f-452e-84cf-4147e6a53501` |

`catalog-facility-prices.csv` は本院 No.1 と No.2 の2院分で、**No.1 は3商品、No.2 は2商品**
（B-1002 は No.2 では扱わない）という形にしてある。医院ごとに扱う商品と単価が違うケースの確認用。

### 最新の一覧を取り直す

```bash
export PATH="/usr/local/opt/postgresql@16/bin:$PATH"
psql -h localhost -U apple -d clinic_inventory -P pager=off \
  -c "SELECT code, name, id FROM distributors ORDER BY code;" \
  -c "SELECT f.name, c.name AS corp, f.id FROM facilities f JOIN corporations c ON c.id=f.corporation_id ORDER BY f.name;"
```

## アップロード

キーの規約は `catalogs/{卸コード}/{ファイル名}.csv`。どの卸のCSVかは**置き場所**で決まる。
卸コードはbackendの`distributors.code`（例: `oroshi-b`）。

```bash
# .env の認証情報を使う
set -a; . ./.env; set +a

aws s3api put-object \
  --bucket "$S3_BUCKET_CATALOGS" \
  --key "catalogs/<卸コード>/catalog-facility-prices.csv" \
  --body samples/catalog-facility-prices.csv \
  --content-type text/csv
```

## 取り込み

```bash
./run.sh -prefix catalogs/<卸コード>/
```

取り込みには`catalogs/`配下への`s3:ListBucket`と`s3:GetObject`が必要
（clinic-inventory の `docs/architecture/s3-storage.md` 3-1章）。付与前は`AccessDenied`になる。

事前に、その卸のCSV読み取り定義を `config/distributor-csv-mappings.json` に用意しておくこと。
