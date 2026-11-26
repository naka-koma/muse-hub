# Phase 5のポリシー提案と承認

[English](phase5-approval.md) | 日本語

[Phase 3のポリシーとジョブDB](phase3-policy.ja.md) を設定します。Hubを起動する前に `MUSE_HUB_PHASE5_APPROVALS=1` を指定してください。初回起動でYAMLの設定をローカルDBに登録し、次回からはDBの有効な設定を使います。YAMLのパスは引き続き指定してください。

## 変更を提案する

`list_policy` が返す `policy` をコピーして編集し、設定全体を `candidate` として `propose_policy` に渡します。たとえば初期設定が `consult: false` の場合、次の提案でダミー相談を有効にできます。

```json
{
  "reason": "review_access",
  "candidate": {
    "version": 1,
    "data": {"code": "full", "work": "none", "health": "none"},
    "execution": {"read": true, "consult": true},
    "resources": ["arithmetic"]
  }
}
```

理由には `reduce_disclosure`、`review_access`、`maintenance` のいずれかを指定します。応答には保留中の提案ID、候補リビジョン、有効期限が含まれます。提案の有効期間は24時間です。権限を広げない提案だけ、起動時に自動で適用できます。自動適用は `auto_applied` として記録します。

## 自宅PCで確認して承認する

Hubを停止します。ジョブDBがあるPCで `MUSE_HUB_PHASE2_DB` にそのパスを指定し、現在の設定と提案内容を確認してください。

```powershell
go run ./cmd/muse-hub policy current
go run ./cmd/muse-hub policy list
go run ./cmd/muse-hub policy approve PROPOSAL_ID CANDIDATE_REVISION
```

最後の行には、確認した提案IDと候補リビジョンを指定します。却下する場合は同じ引数で `policy reject` を使います。承認時には提案ID、候補リビジョン、有効期限、提案の基になったポリシーを確認します。適用すると、変更前のポリシーを基にしたほかの提案は失効します。新しいポリシーで使うにはHubを起動してください。以前の設定を復元しても、変更の影響を受けたレビューセッションは再開できません。

承認はDBにアクセスできるPC上で行います。MCPに承認ツールはなく、HubがDBを使用中は承認コマンドが失敗します。DBはリポジトリの外に保存し、エージェントの実行環境にはマウントしないでください。ジョブ単位の一時許可は引き続き無効です。

## 週次の見直し

```powershell
go run ./cmd/muse-hub policy weekly
```

直近7日間の提案件数と判定件数を、対象期間と有効なリビジョンとともに表示します。個別の提案履歴は `policy list` で確認できます。
