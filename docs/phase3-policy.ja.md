# Phase 3のポリシーと監査の検証

[English](phase3-policy.md) | 日本語

ポリシーは [非同期のダミージョブ](phase2-jobs.ja.md) に適用します。検証には同じ固定の質問を使います。入力は外部のエージェントに送信しません。

## ポリシーの読み込み

次の内容をリポジトリ外に `policy.yaml` として保存します。Hubを起動するPowerShellで `MUSE_HUB_PHASE3_POLICY` にそのファイルのパスを指定してください。Phase 2のDB設定も使います。無効なYAMLを指定するとHubは起動しません。

```yaml
version: 1
data:
  code: full
  creative: summary
  personal: none
agents:
  reviewer:
    code: summary
execution:
  consult: true
  read: true
resources:
  - arithmetic
```

判定では、固定禁止、操作と資源の許可、データごとの開示レベルを順に確認します。`work` と `health` は常に `none` です。未定義カテゴリや、カテゴリを分離できない入力は拒否します。開示レベルは `none`、`summary`、`full` です。`summary` では、信頼できる経路で作った要約だけを扱います。カテゴリの自己申告だけで判断せず、Hubが固定のダミー入力と一致するか確認します。

資源名は論理名です。ファイルへのアクセス権にはなりません。`consult` はジョブの実行、`read` は結果と履歴の取得を制御します。このモードでは、シェル、書き込み、push、一時許可は使えません。

## 拒否と監査件数の確認

Phase 2の手順で `delegate` を呼び、次に未定義のカテゴリや `work` を指定します。どちらも拒否されます。新しい依頼には別の `request_id` を使ってください。`read: false` に変更して再起動すると、すでに完了した結果も取得できなくなります。中断は可能で、回答本文を除いた状態を返します。

`list_policy` は有効な設定、リビジョンID、監査件数を返します。DBの監査テーブルには時刻、操作、リビジョン、分かる場合は既知のカテゴリと開示レベル、判定結果を保存します。依頼本文、回答本文、未定義カテゴリの元の名前は監査テーブルに保存しません。件数には過去のリビジョンでの判定も含まれます。
