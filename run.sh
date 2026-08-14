#!/usr/bin/env bash
# 卸業者がS3に置いた商品マスタCSVを取り込む(cmd/csvsync)。
# 同じディレクトリの .env を読み込んで環境変数にしてから go run する。
# 使い方: ./run.sh
#         ./run.sh -prefix catalogs/<卸ID>/   # 特定の卸だけ取り込む
# 定期実行する場合はこのスクリプトをcron等から呼ぶ(将来はCloud Scheduler + Cloud Functions)。
set -euo pipefail

cd "$(dirname "$0")"

if [ -f .env ]; then
  set -a
  # shellcheck disable=SC1091
  . ./.env
  set +a
else
  echo "警告: .env がありません。.env.example をコピーして作成してください。" >&2
fi

# このマシンではGoのビルドに CGO_ENABLED=0 が必須(詳細は README)。
exec env CGO_ENABLED=0 go run ./cmd/csvsync "$@"
