# S3(発注CSV保管)の設定と運用

このドキュメントは、発注CSVをアップロードするS3バケット・IAMユーザーをなぜ・どう構成したか、および今後の運用手順をまとめる。CSV連携全体の設計意図は`docs/architecture/domain-rules.md`(clinic-inventory側)を参照。

最終更新: 2026-08-29

---

## 1. 基本の考え方

- 発注確定時にバックエンドがCSVを組み立て、S3へアップロードする(コード側は`backend/internal/infrastructure/procurement/purchase_order_csv_uploader.go`(clinic-inventory側))。ユースケースからAWS SDKの`PutObject`に届くまでのコードの追い方は`docs/go/request-to-sql-flow.md`(clinic-inventory側)にまとめてある。
- バックエンドは**IAMユーザーの発行したアクセスキー**でS3に認証する(サーバーがAWS上で動いていないため、IAMロールではなくアクセスキー方式)。
- 権限は最小限に絞っている。現在許可しているのは、このバケットへの`s3:PutObject`・`s3:GetObject`と、`catalogs/`配下に限定した`s3:ListBucket`だけ(削除・他バケットへのアクセスは不可)。

---

## 2. 今回設定した流れ(2026-08-02 に実施済み)

AWSコンソール(アカウント `<AWS_ACCOUNT_ID>`、リージョン `ap-northeast-1`)で一度だけ行った初期設定:

1. **S3バケット作成**
   - バケット名: `clinic-inventory-orders-<AWS_ACCOUNT_ID>`(S3のバケット名は全世界で一意である必要があるため、末尾にAWSアカウントIDを付けた)
   - リージョン: `ap-northeast-1`(アジアパシフィック・東京)
   - パブリックアクセス: 全てブロック(発注データのため非公開が必須)
   - バージョニング: 有効化(誤って上書き・削除した場合の復旧用)
   - 暗号化: デフォルト(SSE-S3)
2. **IAMユーザー作成**
   - ユーザー名: `clinic-inventory-backend`
   - コンソールログイン権限は付与せず、プログラムからのアクセス(アクセスキー)のみ
3. **インラインポリシーを付与**(最小権限)
   - ポリシー名: `clinic-inventory-backend-s3-orders`
   - 内容:
     ```json
     {
       "Version": "2012-10-17",
       "Statement": [
         {
           "Effect": "Allow",
           "Action": "s3:PutObject",
           "Resource": "arn:aws:s3:::clinic-inventory-orders-<AWS_ACCOUNT_ID>/*"
         }
       ]
     }
     ```
4. **アクセスキー発行**
   - 対象ユーザーの「セキュリティ認証情報」タブ→「アクセスキーを作成」→ユースケース「ローカルコード」
   - 発行されたAccess Key ID / Secret Access Keyは表示直後にしか確認できない(AWS側の仕様)
5. **`backend/.env` に設定**(値は`.env.example`参照。実値は秘匿情報のためリポジトリには含めない)
   ```
   AWS_ACCESS_KEY_ID=...
   AWS_SECRET_ACCESS_KEY=...
   AWS_REGION=ap-northeast-1
   S3_BUCKET_ORDERS=clinic-inventory-orders-<AWS_ACCOUNT_ID>
   ```
6. **動作確認**: 同じ`.env`の値を使い、`aws s3api put-object`でテストアップロードが成功することを確認済み(`_healthcheck/test.txt`というテスト用オブジェクトがバケットに残っている。削除権限を持たせていないため、不要であればAWSコンソールから手動削除する)。

これらは**初回だけ**の作業。以後発注が増えても再設定は不要。

---

## 3. S3キーの命名規則

```
orders/{卸コード}/{facilityId}/{orderId}.csv   ← 発注CSV(こちらが書く)
catalogs/{卸コード}/{任意のファイル名}.csv      ← 商品マスタCSV(卸が置き、こちらが読む)
```

フォルダ名にはUUIDではなく**卸コード**(`distributors.code`。例: `oroshi-b`)を使う。卸業者自身に「あなたのフォルダはここです」と案内する場面で、人が読める識別子である必要があるため。コードは小文字英数字とハイフンに制限している(URLエンコードや大文字小文字の扱いで事故らないようにするため)。

卸業者ごとにフォルダ(プレフィックス)を分けている。将来、卸業者側にS3への読み取り権限を渡す場合は、このプレフィックス単位でIAMポリシーを分ければ「自社宛のフォルダしか見えない」テナント分離ができる(現時点では卸側へのアクセス権限はまだ設定していない)。

商品マスタCSVも同じ規約にしているため、**どの卸のCSVかは中身ではなく置かれた場所で決まる**。商品マスタ側から見た設計は[distributor-catalog-import.md](distributor-catalog-import.md)、取り込み処理そのものは[design.md](design.md)を参照。

---

## 3-1. 商品マスタCSVの取り込みに必要な権限追加(2026-08-14 実施済み)

取り込みバッチ(このリポジトリ)は`catalogs/`配下を一覧して各CSVを読むため、当初の`s3:PutObject`のみのポリシーでは`AccessDenied`になっていた。AWSコンソールでIAMユーザー`clinic-inventory-backend`のインラインポリシー`clinic-inventory-backend-s3-orders`を以下に差し替え済み。

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:PutObject", "s3:GetObject"],
      "Resource": "arn:aws:s3:::clinic-inventory-orders-<AWS_ACCOUNT_ID>/*"
    },
    {
      "Effect": "Allow",
      "Action": "s3:ListBucket",
      "Resource": "arn:aws:s3:::clinic-inventory-orders-<AWS_ACCOUNT_ID>",
      "Condition": { "StringLike": { "s3:prefix": ["catalogs/*"] } }
    }
  ]
}
```

- `GetObject`/`PutObject`はバケット内のオブジェクトが対象なので`/*`付き、`ListBucket`はバケット自体が対象なので`/*`無し(AWSの仕様上この2つはリソースの書き方が違う)。
- 一覧は`catalogs/`配下だけに絞っている。取り込み処理はそこしか見ないため、発注CSVの一覧は許可しない(最小権限)。

### 差し替えの手順(AWSコンソール)

1. IAMコンソールを開く → 左メニュー「ユーザー」→ **`clinic-inventory-backend`** をクリック
   （直リンク: `https://console.aws.amazon.com/iam/home#/users/clinic-inventory-backend`）
2. 「許可」タブ → 許可ポリシーの一覧にある **`clinic-inventory-backend-s3-orders`**（種類: インライン）の行を開く
3. 「編集」→ エディタを **JSON** タブに切り替える
4. 中身を全選択して削除し、上のJSONを貼り付ける
5. 「次へ」→ 内容を確認して「変更を保存」
6. 反映は即時。ターミナルで確認する:
   ```bash
   cd backend && set -a && . ./.env && set +a
   aws s3api list-objects-v2 --bucket "$S3_BUCKET_ORDERS" --prefix catalogs/ --max-items 5
   ```
   `AccessDenied`が消え、オブジェクトの一覧が返れば成功。

> IAMユーザー自体の作成やアクセスキーの発行は不要(2章で実施済み)。**既存のポリシーの中身を差し替えるだけ**。

---

## 4. 今後の運用

### アクセスキーを漏洩・失効させたい場合
1. IAMコンソール→ユーザー`clinic-inventory-backend`→「セキュリティ認証情報」タブ
2. 該当のアクセスキーを「非アクティブ化」(即座に使えなくなる。動作確認後に問題なければ「削除」)
3. 新しいアクセスキーを発行し、`backend/.env`を更新

### 権限が足りないエラーが出た場合(`AccessDenied`)
- 現在許可しているのは`s3:PutObject`・`s3:GetObject`と、`catalogs/`配下限定の`s3:ListBucket`(3-1章)。他の操作(削除・`catalogs/`以外の一覧)が必要な機能を追加する場合は、上記インラインポリシーに`Action`を追記する(必要な操作だけを都度足す方針。最初から広く許可しない)。

### 本番環境を分ける場合
- 開発用(`clinic-inventory-orders-<AWS_ACCOUNT_ID>`)とは別に、本番用バケット・IAMユーザーを新規に作成する(同じ手順を繰り返す)。バケット名がアカウントIDベースのため、本番が別AWSアカウントなら重複の心配はない。
- サーバーをAWS上(ECS/EC2等)で動かす場合は、アクセスキー方式ではなく**IAMロール**への切り替えを検討する(キーの管理・ローテーションが不要になるため、より安全)。

---

## 5. よくある疑問

- **Q. なぜS3の「フォルダ」を事前に作らなかった?**
  A. S3はディレクトリ構造を持たないフラットなキーバリューストアのため、フォルダを事前に作る操作は存在しない。`orders/{卸コード}/...`というキーでオブジェクトをアップロードした瞬間に、コンソール上「フォルダができた」ように見えるだけ。

- **Q. IAMユーザーではなくIAMロールにすべきでは?**
  A. 理想はロールだが、ロールはAWSのコンピューティングサービス(EC2/ECS/Lambda等)上で動くリソースに付与する仕組み。現状バックエンドはローカル/任意のサーバーで動かす想定のため、アクセスキー方式のIAMユーザーにしている。AWS上にデプロイする段階でロールへの切り替えを検討する(上記4章参照)。
