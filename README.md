# HPCGame Kube CLI

HPCGame kube 平台的命令行工具，提供更加灵活的与平台交互功能。本版本同时支持原始命令和类 Docker 风格命令，让用户可以根据自己的习惯选择操作方式。该工具可以快速配置环境、创建容器、管理文件和连接容器，简化了 Kubernetes 的操作流程，让参赛者能够专注于比赛任务而非基础设施管理。

## 主要功能

- 安装和配置 kubectl、VSCode 插件
- 简化容器创建和管理
- 提供命令行的容器交互
- 文件传输和端口转发
- 持久卷管理
- 与 Docker 命令兼容的替代命令

## 安装

从 [releases](https://github.com/lcpu-club/hpcgame-kube-cli/releases) 页面下载最新版本的二进制文件，解压后将其放置在 PATH 中。

选择适合操作系统的压缩包：

- Linux: hpcgame-linux-amd64.tar.gz 或 hpcgame-linux-arm64.tar.gz
- macOS: hpcgame-darwin-amd64.tar.gz 或 hpcgame-darwin-arm64.tar.gz
- Windows: hpcgame-windows-amd64.zip

## 命令对照表

| 原始命令 | Docker 风格替代命令 | 说明 |
|---------|-------------------|------|
| create | run | 创建新容器 |
| ls | ps | 列出当前容器 |
| lspart | images | 列出可用分区/镜像 |
| shell | exec -it | 连接到容器终端 |
| delete | rm | 删除容器 |
| portforward | port | 设置端口转发 |
| volume | volume | 管理持久卷 |

## 使用流程

要获取任何命令的详细帮助，请使用：
```bash
hpcgame help
```

### 初始化环境

首先，需要初始化环境。如果您使用 VSCode Remote 连接到 Linux 服务器，并在其上开发，可以直接在 VSCode 终端中执行如下命令：

```bash
hpcgame install
```

这个命令会：

- 检查并安装 kubectl（如果需要）
- 提示输入比赛平台提供的 kubeconfig
- 验证并保存 kubeconfig
- 安装必要的 VSCode 扩展（如果可用）
- 显示可用的计算分区信息

### 查看分区

查看可用的计算分区：

```bash
# 命令
hpcgame lspart
```

### 创建容器

创建一个新的容器有两种方式：

#### 命令：

```bash
hpcgame create --partition=x86 --cpu=4 --memory=8 --gpu=1 --image=ubuntu:24.04 --name=my-container
```

也可以使用短命令格式：
```bash
hpcgame create -p x86 -c 4 -m 8 -g 1 -i ubuntu:24.04 -n my-container
```

#### 使用 Docker 风格命令：

```bash
hpcgame run -p gpu -c 2 -m 4 -g 1 -n my-container pytorch/pytorch
```

参数说明：

```
-p, --partition: 分区名称（如 x86, gpu 等）
-c, --cpu: CPU 核心数
-m, --memory: 内存大小（GiB）（默认为 CPU×2）
-g, --gpu: GPU 数量（默认为 0）
-d, --duration: Pod 最长运行时间（秒）（默认为 7200，即 2 小时）
-i, --image: 容器镜像（create 命令）
-n, --name: 容器名称（默认自动生成）
-v, --volume, --volumes: 要挂载的额外持久卷（逗号分隔）
```

### 查看容器

列出容器：

```bash
# 命令
hpcgame ls

# Docker 风格替代命令
hpcgame ps
```

### 连接到容器

打开容器的 shell 终端：

```bash
# 命令
hpcgame shell my-container

# Docker 风格替代命令
hpcgame exec -it my-container bash
```

### 在容器中执行命令

在不进入 shell 的情况下执行命令：

```bash
# 命令
hpcgame exec my-container ls -la

# Docker 风格替代命令
hpcgame exec my-container ls -la
```

### 传输文件

在本地和容器之间复制文件：

从本地到容器：
```bash
hpcgame cp ./local-file.txt my-container:/path/to/destination/
```

从容器到本地：
```bash
hpcgame cp my-container:/path/to/file.txt ./local-destination/
```

### 端口转发

将容器端口映射到本地端口：

```bash
# 命令
hpcgame portforward my-container 8080:80

# Docker 风格替代命令
hpcgame port my-container 8080:80
```

这会将容器的 80 端口映射到本地的 8080 端口。

### 管理持久卷

列出持久卷：

```bash
hpcgame volume ls
```

创建新的持久卷：

```bash
hpcgame volume create my-data 10Gi x86-amd-default-sc ReadWriteMany
```

删除持久卷：

```bash
# 命令
hpcgame volume delete my-data

# Docker 风格替代命令
hpcgame volume rm my-data
```

### 删除容器

删除不再需要的容器：

```bash
# 命令
hpcgame delete my-container

# Docker 风格替代命令
hpcgame rm my-container
```

## 注意事项

- 各分区对 CPU, 内存和 GPU 有资源限制
- 默认分区持久卷会自动挂载到 `/partition-data`（默认工作目录）
- 额外指定的持久卷会挂载到 `/mnt/<持久卷名称>`
- 默认持久卷（名称包含 `-default-pvc` 的）不能被删除
- 文件传输和连接操作需要容器处于运行状态
- 对于熟悉 Docker 的用户，可以使用 Docker 风格的命令（run、ps、exec、rm 等）
- 对于已经熟悉原始 HPCGame 命令的用户，所有原始命令继续保持有效

## 问题反馈

如有问题或建议，请在 [GitHub Issues](https://github.com/lcpu-club/hpcgame-kube-cli/issues) 页面提交反馈。