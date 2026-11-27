# Phase 2の非同期ジョブ検証

[English](phase2-jobs.md) | 日本語

Phase 2では、固定のダミー質問で非同期ジョブを検証します。既定ではHub内のワーカーが約1秒後に `5` を返します。

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
- SQLiteにはジョブID、時刻、状態、結果、エージェント名、再送判定用のハッシュを保存します。依頼IDとセッションIDの元の文字列や、入力本文は保存しません。完了から7日経過したジョブと再送記録は、次の起動時またはアクセス時に削除します。

## Windowsで子プロセスの停止を確認する

ジョブDBを指定したうえで、Hubの起動前に `MUSE_HUB_PHASE2_SUBPROCESS=1` を設定します。同じダミー処理を約5秒かけて実行します。Hubは固定の補助プロセスとその子をWindowsのJob Objectにまとめます。ツールの引数をコマンドとして実行せず、Hubのトークンも補助プロセスに渡しません。

新しい `request_id` でダミージョブを開始し、実行中に返されたIDを `cancel_job` に渡してください。補助プロセスが子の終了を待ち、Hubが親子の停止を確認できた場合に `cancelled` を返します。停止を確認できなかった場合は `failed` になります。設定を外して起動すると、Hub内でダミー処理を実行します。

## WindowsでCodexへの委譲を確認する

リポジトリの外にCodex専用ディレクトリを作ります。別のPowerShellで `CODEX_HOME` にそのディレクトリを指定し、`codex login` でログインします。Hubの起動時は、`MUSE_HUB_CODEX_HOME` に同じディレクトリを、`MUSE_HUB_CODEX_EXE` にWindows版 `codex.exe` の絶対パスを指定します。ジョブDBも設定してください。

`delegate` を `agent: "codex"`、`task: "2 + 3"`、空の `context`、`data_category: ["code"]`、新しい `request_id` で呼びます。返ったIDを `get_result` に渡すと、完了時に `succeeded` と結果 `"5"` が返ります。Hubが送るのは固定の計算問題です。シェルやWeb検索などを無効にし、回答は `"5"` だけを受け付けます。ポリシーを使う場合は、Codexに `code` カテゴリと `arithmetic` リソースを許可します。

### 文章や文脈をCodexに送る

Codexの設定を済ませ、ポリシーを読み込まないHubの起動時に `MUSE_HUB_CODEX_TEXT=1` を設定します。`task` は必須で最大4 KiB、参照用の `context` は任意で最大16 KiBです。上限はUTF-8のバイト数で数えます。`data_category: ["code"]` を指定し、依頼や文脈を変えたら新しい `request_id` を使います。

```json
{"agent":"codex","task":"contextを1文で要約して","context":"今回のビルドは2分で終わった。前回は5分かかった。","data_category":["code"],"request_id":"summary-1"}
```

Codexに共有できる文章を指定してください。CLIには標準入力で文章を渡し、計算問題と同じツール制限を使います。Hubは最大16 KiBの回答を保存し、`get_result` で返します。ポリシーを読み込んだHubでは固定の計算問題だけを受け付け、過去の自由入力への回答の取得も拒否します。
