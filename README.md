# clinic-inventory-csv-functions

社外システム(卸業者)とのCSV連携を、スケジュール実行のバッチとして動かすためのリポジトリ。

**卸 → clinic-inventory 方向のCSV取り込み**を担う。卸がAWS S3に置いたCSVを定刻に読み取り、
clinic-inventory のDBへ反映する。

```
卸 ──CSVをアップロード──▶ AWS S3 ──定刻に起動──▶ このバッチ ──▶ clinic-inventory のDB
```

## 扱う範囲

| CSV | 内容 | 状況 |
|---|---|---|
| 商品マスタ | 卸の取扱商品 → `distributor_products` / `distributor_product_facility_prices` | 実装済み |
| 受注確定 | 発注に対する卸からの確定内容（欠品・確定数量） | 未着手 |
| 売上明細 | 卸からの売上明細 → 単価の確定 | 未着手 |

単価を公表しない卸があるため、その商品は標準単価を**NULL**（非公表）で登録する。
後から届くデータで単価を確定する流れは今後の検討事項。

## clinic-inventory との関係

| | clinic-inventory (backend) | このリポジトリ |
|---|---|---|
| テーブルの作成・変更 | **こちらが行う**（AutoMigrate） | 行わない |
| 業務データの参照・更新API | こちらが持つ | 持たない |
| S3からのCSV取り込み | 行わない | **こちらが行う** |
| DBへの書き込み | 画面・API経由の操作 | 取り込み結果の反映 |

DBは同じものを共有する。**テーブルの所有者はbackendのみ**で、このリポジトリはマイグレーションを
一切持たない（既にあるテーブルへ読み書きするだけ）。backend側で列を変更した場合は、
`internal/infrastructure/*/model.go` の対応する構造体も合わせて更新する。

反映先の集約（`DistributorProduct` / `FacilityPrice`）はbackendにも同じものがあり、
業務ルール（必須項目・単価の扱い・廃盤）は揃える必要がある。

## ビルド・テスト

このマシンでは `go test` / `go build` をcgo有効（デフォルト）で実行すると、テストバイナリが
`dyld: missing LC_UUID load command` で異常終了する（ローカル環境の問題で、コードの不具合ではない）。
**`CGO_ENABLED=0` を付けて実行すること**。

```bash
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go test ./...
```

DBを使うテストは、接続できない場合・テーブルが未作成の場合はスキップされる
（テーブルはbackendを起動すると作られる）。

## 実行

```bash
cp .env.example .env   # 初回のみ。値を埋める
./run.sh                                    # catalogs/ 配下を全件チェックして取り込む
./run.sh -prefix catalogs/<卸コード>/       # 特定の卸だけ
```

出力は「反映 / 要確認 / スキップ(取り込み済み)」の3種類。詳細は
`distributor_catalog_ingestion_runs` テーブルと `distributor_catalog_staging_rows` テーブルで確認する。

必要なAWSの権限は `catalogs/` 配下への `s3:ListBucket` と `s3:GetObject`。

## 設計

- [docs/design.md](docs/design.md) … 取り込みの全体像・卸ごとのCSV形式差の吸収方法・単価の3パターン・失敗時の扱い（なぜそうしたか）
- [docs/csv-to-db-flow.md](docs/csv-to-db-flow.md) … 卸BのCSV1本がDBに入るまでを実データで追う（何がどう動くか）

## ディレクトリ構成

```
cmd/csvsync/          定期実行の入口（配線のみ）
internal/
  domain/             集約と業務ルール
    distributorcatalog/     反映先（卸商品・医院別単価）
    distributorcsvingestion/取り込み実行とステージング行
  application/        ユースケースとポート（中間表現の定義もここ）
  infrastructure/
    distributorcsvingestion/
      catalog_<卸コード>.go  卸別パーサ（1社1ファイル）
      parser_registry.go    卸コード → パーサの対応表
      csv_util.go           パーサ共通の部品
    storage/ database/ …    S3・DBなどの実装
```

新しい卸に対応するときは `catalog_<卸コード>.go` を1つ書き、`parser_registry.go` の
`DefaultParsers()` に1行足す。中間表現より後ろ（DB反映）は全卸共通で、変更不要。
