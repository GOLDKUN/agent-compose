# V2 存储迁移

对于具有合法 `schema_migrations` 前缀的数据库，daemon 会原地升级。`000001` 至 `000004` 保持不可变；`000005` 至 `000007` 将业务 v1 的 agent、loader 和 event link 存储替换为原生的 project agent、scheduler 与 sandbox 关联。

升级前请停止旧 daemon，并备份完整 data root。只包含 project-managed agent/scheduler 的正常 versioned 数据库可以直接由新 daemon 打开。

旧目录包含 standalone agent/loader、没有 migration history 但属于可识别的 legacy shape，或仍需转换旧文件布局时，使用独立复制 migrator。versioned source 必须具有已知且完全匹配的 migration prefix；checksum 不一致时会拒绝处理，不会猜测：

```bash
agent-compose-migrate --source /old/data-root --target /new/data-root --dry-run
agent-compose-migrate --source /old/data-root --target /new/data-root --json
```

source 以只读方式打开。target 必须是新目录，或带有相同 source fingerprint 的 migrator journal；不提供覆盖参数。migrator 会对 SQLite 一致性快照和权威文件计算 fingerprint，将 standalone 或 mixed agent/scheduler 转为 `legacy-v1-default` project；历史 managed projection 与其 revision 不一致时，会追加新的 immutable revision。只有持久化的 event/sandbox 证据唯一指向某个 scheduler run 时，才会回填 project run 的 scheduler-run 关联；无法证明或存在歧义的关联保持 `NULL`，并写入报告。

文件复制不会跟随 symlink。旧 `sessions`、`loaders` 目录会分别转换为 `sandboxes`、`schedulers`，旧 loader 目录 ID 会映射为 scheduler ID，旧 data root 下的数据库路径也会改写到 target。新旧布局发生目标冲突时会直接失败，不会互相覆盖。匹配的 journal 支持中断续跑；逻辑数据库或权威文件发生变化后则拒绝续跑。source 始终保留用于回滚。成功后由 operator 显式将 daemon 指向 target。

如果报告指出 shape 不受支持或归属存在歧义，不要让新 daemon 直接读取旧目录。daemon 启动时不会猜测归属，也不会迁移 unversioned schema。
