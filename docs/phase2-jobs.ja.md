# Phase 2の非同期ジョブ検証

[English](phase2-jobs.md) | 日本語

Phase 2では、固定のダミー質問で非同期ジョブを検証します。Hub内のワーカーが約1秒後に `5` を返します。外部のエージェントは呼び出しません。

## Hubの起動

[README](../README.ja.md) の手順で `MUSE_HUB_TOKEN` を設定します。同じPowerShellで、リポジトリ外にSQLiteファイルの保存先を指定し、Hubを起動してください。

```powershell
$jobDir = Join-Path $env:LOCALAPPDATA 'MuseHub'
New-Item -ItemType Directory -Path $jobDir -Force | Out-Null
$env:MUSE_HUB_PHASE2_DB = Join-Path $jobDir 'phase2-jobs.sqlite'
go run ./cmd/muse-hub
```

`MUSE_HUB_PHASE1_AUTH_VOLUME` は未設定にしてください。Phase 2では `ping`、`echo` に加え、`delegate`、`get_result`、`cancel_job`、`list_jobs` が使えます。Museからの接続は [Tailscale Serveの手順](usage.ja.md#tailscale-serve) に従います。

## ツールの呼び出し

次の引数で `delegate` を呼びます。新しいジョブごとに別の `request_id` を作り、同じ依頼を再送するときだけ再利用してください。`session` は任意です。同じセッションで実行できるジョブは1件です。

```json
{"agent":"dummy","task":"2 + 3","context":"","data_category":["code"],"request_id":"sample-1","session":"demo"}
```

`delegate` はすぐに `job_id` を返します。次のターンでそのIDを `get_result` に渡してください。完了すると `status: "succeeded"` と `result: "5"` が返ります。中断する場合は、同じIDを `cancel_job` に渡します。ワーカーの停止を確認してから状態を返します。`list_jobs` では結果本文を除いた直近100件を確認できます。

- 同時に実行できるジョブは最大5件です。同じセッションで別の依頼が実行中なら `session_busy`、同じ `request_id` を別の依頼に使った場合は `request_id_conflict` が返ります。
- タイムアウトは30秒です。起動時に待機中または実行中のジョブが残っていれば `interrupted` にします。同じサンプルをもう一度実行するには、新しい `request_id` を指定してください。
- SQLiteにはジョブID、時刻、状態、ダミー結果、再送判定用のハッシュを保存します。依頼IDとセッションIDの元の文字列や、入力本文は保存しません。完了から7日経過したジョブと再送記録は、次の起動時またはアクセス時に削除します。

## Windowsで子プロセスの停止を確認する

ジョブDBを指定したうえで、Hubの起動前に `MUSE_HUB_PHASE2_SUBPROCESS=1` を設定します。同じダミー処理を約5秒かけて実行します。Hubは固定の補助プロセスとその子をWindowsのJob Objectにまとめます。ツールの引数をコマンドとして実行せず、Hubのトークンも補助プロセスに渡しません。

新しい `request_id` でダミージョブを開始し、実行中に返されたIDを `cancel_job` に渡してください。補助プロセスが子の終了を待ち、Hubが親子の停止を確認できた場合に `cancelled` を返します。停止を確認できなかった場合は `failed` になります。設定を外して起動すると、Hub内でダミー処理を実行します。
