# ILM Tiering Design [![slack](https://slack.min.io/slack?type=svg)](https://slack.min.io) [![Docker Pulls](https://img.shields.io/docker/pulls/minio/minio.svg?maxAge=604800)](https://hub.docker.com/r/minio/minio/)

在 [bucket lifecycle guide](https://github.com/minio/minio/master/docs/bucket/lifecycle/README.md) 中提供的生命周期迁移功能，允许将内容从 MinIO 对象存储分层到公有云或其他 MinIO 集群。

Transition tiers can be added to MinIO using `mc admin tier add` command to associate a `gcs`, `s3` or `azure` bucket or prefix path on a bucket to the tier name.
可以使用 mc admin tier add 命令将迁移层添加到 MinIO，以将 gcs、s3 或 azure 存储桶或桶中的前缀路径与层名称关联。
Lifecycle transition rules can be applied to buckets (both versioned and un-versioned) by specifying the tier name defined above as the transition storage class for the lifecycle rule.
通过在生命周期规则中指定定义的层名称作为迁移存储类，可以将生命周期迁移规则应用于（有版本控制和无版本控制的）桶。

## 实现

当桶中存放的对象符合生命周期迁移规则并符合分层条件时，ILM 分层将开始。MinIO 扫描器（每隔一分钟运行一次，每次扫描整个命名空间的十六分之一）会选择符合条件的对象进行分层。数据将整体迁移到远程分层存储上，仅在 MinIO 上保留对象的元数据.

The data on the backend is stored under the `bucket/prefix` specified in the tier configuration with a custom name derived from a randomly generated uuid - e.g. `0b/c4/0bc4fab7-2daf-4d2f-8e39-5c6c6fb7e2d3`. The first two prefixes are characters 1-2,3-4 from the uuid. This format allows tiering to any cloud irrespective of whether the cloud in question supports versioning. The reference to the transitioned object name and transitioned tier is stored as part of the internal metadata for the object (or its version) on MinIO.

在后端，数据存储在分层配置中指定的 bucket/prefix 路径下，文件名基于随机生成的 uuid 定制生成，例如 0b/c4/0bc4fab7-2daf-4d2f-8e39-5c6c6fb7e2d3。前两个前缀是 uuid 的第 1-2 位和第 3-4 位字符。这种格式允许分层到任何云存储，无论该云存储是否支持版本控制。对已迁移对象的名称和迁移层的引用作为对象（或其版本）的内部元数据存储在 MinIO 上。

迁移对象的额外元数据存储在 xl.meta 文件中：

```
...
        "MetaSys": {
          "x-minio-internal-transition-status": "Y29tcGxldGU=",
          "x-minio-internal-transition-tier": "R0NTVElFUjE=",
          "x-minio-internal-transitioned-object": "ZDIvN2MvZDI3Y2MwYWMtZGIzNC00ZGM1LWIxNDUtYjI5MGNjZjU1MjY5"
        },
```

当通过 PostRestoreObject API 暂时将已迁移对象恢复到本地 MinIO 实例时，对象数据将从远程层复制回本地，并维护恢复对象的额外元数据，如下所示。一旦恢复期结束，扫描器在其定期运行中会删除本地对象副本。

```
...
        "MetaUsr": {
         "X-Amz-Restore-Expiry-Days": "4",
          "X-Amz-Restore-Request-Date": "Mon, 22 Feb 2021 21:10:09 GMT",
          "x-amz-restore": "ongoing-request=false, expiry-date=Sat, 27 Feb 2021 00:00:00 GMT",
...
```

### 加密/对象锁定的对象

对于使用 SSE-S3 或 SSE-C 加密的对象，来自 MinIO 集群的加密内容将直接复制到远程分层存储中，不会解密。当从远程分层存储中流式读取内容时（通过 GET/HEAD 请求），会自动解密。对于处于保留状态的对象，其元数据会确保在保留期结束前不会被删除。管理员需要确保远程分层存储桶具备适当的访问控制。

### 迁移状态

在 HEAD/GET 请求中会显示 MinIO 特有的扩展头部 X-Minio-Transition，以预估对象的预计迁移日期。一旦对象已迁移到远程层，x-amz-storage-class 会显示对象已迁移到的层名称。当对象处于恢复状态或已恢复到本地 MinIO 集群时，额外的头部如 X-Amz-Restore-Expiry-Days、x-amz-restore 和 X-Amz-Restore-Request-Date 将会显示。

### 过期或删除事件

一旦对象到达其过期日期，或通过 mc rm（对于特定对象版本的删除，使用 mc rm --vid）命令删除，对象将从分层存储中被删除。. Other rules specific to legal hold and object locking precede any lifecycle rules.

### 其他说明

分层和生命周期迁移仅适用于纠删码/分布式 MinIO 部署。

## 进一步探索

- [MinIO | Golang 客户端 API 参考](https://min.io/docs/minio/linux/developers/go/API.html#setbucketlifecycle-ctx-context-context-bucketname-config-lifecycle-configuration-error)
- [对象生命周期管理](https://docs.aws.amazon.com/AmazonS3/latest/dev/object-lifecycle-mgmt.html)
