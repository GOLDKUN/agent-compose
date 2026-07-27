# V2 存储迁移

对于具有合法 `schema_migrations` 前缀的数据库，daemon 会原地升级。`000001` 至 `000004` 保持不可变；`000005` 至 `000007` 将业务 v1 的 agent、loader 和 event link 存储替换为原生的 project agent、scheduler 与 sandbox 关联。

升级前请先用旧 CLI 停止所有 sandbox，再停止旧 daemon，并备份完整 data root。必须先停止 sandbox，因为已有 Docker 容器仍会保留指向旧目录的 bind mount；复制 migrator 会拒绝 running sandbox metadata，避免新旧目录同时被写入。切换后恢复已停止的 legacy sandbox 时，会使用 target root mount 安全重建其容器。只包含 project-managed agent/scheduler 的正常 versioned 数据库可以直接由新 daemon 打开。

旧目录包含 standalone agent/loader、没有 migration history 但属于可识别的 legacy shape，或仍需转换旧文件布局时，使用独立 migrator。versioned source 必须具有已知且完全匹配的 migration prefix；checksum 不一致时会拒绝处理，不会猜测。推荐将同一个 data root 同时传给 `--source` 和 `--target`，执行原地迁移：

```bash
agent-compose-migrate --source /data --target /data --dry-run
agent-compose-migrate --source /data --target /data --json
```

dry-run 会在临时数据库中演练数据库转换，并校验每个文件系统条目和路径改写，但不会复制 sandbox、workspace、volume、artifact、image 或 cache 数据。原地执行时，会在 `.agent-compose-migrate-backup` 下保留一致的 v1 数据库以及被改写 JSON 的原文件，将 `sessions` rename 为 `sandboxes`，将 `loaders` 及其目录 ID rename 为原生 scheduler 路径，最后再原子切换转换后的数据库。大文件始终保留在同一文件系统和 inode 上。

发布的 daemon 镜像包含此命令。使用受支持的镜像部署方式时，将已停止 daemon 的 data root 以可写方式挂载到 `/data`（将 `VERSION` 替换为待安装版本）：

```bash
docker run --rm --entrypoint agent-compose-migrate \
  -v /old/data-root:/data \
  ghcr.io/chaitin/agent-compose:VERSION \
  --source /data --target /data --runtime-root /data --dry-run
```

只有 dry-run 成功后，才使用完全相同的命令移除该参数并正式执行。在迁移完成前保持旧 daemon 停止。匹配的 journal 可以从中断的数据库、目录布局或数据库激活阶段继续执行。在新 daemon 和迁移数据验证完成前，不要删除 backup 目录。

如果 operator 明确需要单独的 target，仍可让 `--source` 和 `--target` 指向不同目录，并用 `--runtime-root` 指定新 daemon 最终看到的路径。复制模式会保留 source 用于回滚，但需要足够空间复制全部文件。

migrator 会对 SQLite 一致性快照和权威文件计算 fingerprint，将 standalone 或 mixed agent/scheduler 转为 `legacy-v1-default` project；历史 managed projection 与其 revision 不一致时，会追加新的 immutable revision。只有持久化的 event/sandbox 证据唯一指向某个 scheduler run 时，才会回填 project run 的 scheduler-run 关联；无法证明或存在歧义的关联保持 `NULL`，并写入报告。

文件系统校验和复制模式都不会跟随 symlink。旧 `sessions`、`loaders` 目录会分别转换为 `sandboxes`、`schedulers`，旧 loader 目录 ID 会映射为 scheduler ID；数据库存储路径以及已知 sandbox lifecycle、metadata、mount manifest 中的路径字段会改写到 runtime root。无关 JSON 字符串和外部 volume 路径保持不变。原地 rename 前会先拒绝任何新旧布局冲突，不会互相覆盖。复制模式下，匹配的 journal 支持中断续跑，source 始终保持不变。使用独立 target 迁移成功后，由 operator 显式将 daemon 指向该 target root。

如果报告指出 shape 不受支持或归属存在歧义，不要让新 daemon 直接读取旧目录。daemon 启动时不会猜测归属，也不会迁移 unversioned schema。
