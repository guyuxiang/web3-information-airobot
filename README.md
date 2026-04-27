# AI Web3 News Filter

Golang 服务轮询 TechFlow 的 RSS (`https://www.techflowpost.com/rss.aspx`)，调用 AI 分析资讯，筛选以下类型：

- 重要政策/监管动向
- 行业重点项目、重大落地
- 创新新兴赛道或生态热点
- RWA 与支付赛道
- 投融资与机构动作
- 安全事件

符合条件的资讯存储在 MySQL 中，并通过 `/items` 接口返回，同时支持每日邮件推送。

## 快速开始

```bash
# AI API 配置
export OPENAI_API_KEY=<your-api-key>
export OPENAI_BASE_URL=https://api.deepseek.com
export OPENAI_MODEL=deepseek-v4-flash
# MySQL 连接信息
export DB_HOST=mysql01.dev.lls.com
export DB_PORT=4120
export DB_USER=root
export DB_PASSWORD=123456
export DB_NAME=aiweb3news
go run ./cmd/aiweb3news
# 服务默认监听 :8082
```

## 可选环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `FEED_URL` | RSS 地址 | `https://www.techflowpost.com/rss.aspx` |
| `POLL_INTERVAL_MINUTES` | 轮询间隔（分钟） | `15` |
| `BIND_ADDR` | HTTP 监听地址 | `:8082` |
| `OPENAI_API_KEY` | AI API Key | — |
| `OPENAI_MODEL` | AI 模型 | `deepseek-v4-flash` |
| `OPENAI_BASE_URL` | AI API 地址 | `https://api.deepseek.com` |
| `MAX_ITEMS` | 接口返回结果条数上限 | `50` |
| `DB_HOST` | MySQL 地址 | `mysql01.dev.lls.com` |
| `DB_PORT` | MySQL 端口 | `4120` |
| `DB_USER` | MySQL 用户名 | `root` |
| `DB_PASSWORD` | MySQL 密码 | `123456` |
| `DB_NAME` | MySQL 数据库名 | `aiweb3news` |
| `EMAIL_SMTP_HOST` | SMTP 服务器地址 | — |
| `EMAIL_SMTP_PORT` | SMTP 端口 | `587` |
| `EMAIL_SMTP_USER` | SMTP 用户名 | — |
| `EMAIL_SMTP_PASSWORD` | SMTP 密码 | — |
| `EMAIL_FROM` | 发件人地址 | — |
| `EMAIL_FROM_NAME` | 发件人名称 | — |
| `EMAIL_TO` | 收件人（多个用逗号分隔） | — |
| `EMAIL_SEND_HOUR` | 每日发送时间（北京时间） | `9` |

## HTTP 接口

- `GET /healthz`：健康检查
- `GET /items`：返回筛选结果，字段包含标题、链接、发布时间、分类、理由及标签

## 工作流

1. 定时拉取 RSS
2. 将每条资讯摘要与元信息发送到 AI：
   - 判断是否属于指定类型
   - 返回分类、理由、标签
3. 将所有分析结果存入 MySQL（表：`news_analysis`），接口 `/items` 读取数据库返回"相关"资讯
4. 每天定时（北京时间 `EMAIL_SEND_HOUR` 点）发送邮件日报，汇总前一天的相关资讯

## 邮件推送

配置 SMTP 环境变量后，服务会自动在每天指定时间发送日报邮件。

也可通过测试命令手动发送：

```bash
EMAIL_SMTP_HOST=smtp.exmail.qq.com \
EMAIL_SMTP_PORT=465 \
EMAIL_SMTP_USER=projectc-test@linklogis.com \
EMAIL_SMTP_PASSWORD=<password> \
EMAIL_FROM=projectc-test@linklogis.com \
EMAIL_FROM_NAME=CSOInfoAssist \
go run ./cmd/testemail/
```

## Docker 运行

```bash
docker run -d -p 8082:8082 --name aiweb3news \
  -e OPENAI_API_KEY=<your-api-key> \
  -e OPENAI_MODEL=deepseek-v4-flash \
  -e OPENAI_BASE_URL=https://api.deepseek.com \
  ghcr.io/guyuxiang/aiweb3news:latest
```

### 查看日志

```bash
docker logs -f --tail 100 aiweb3news
```

### 自定义配置

可以通过 `-e` 覆盖环境变量，完整列表见上方可选环境变量表格。

## 开发提示

- 需要网络访问 RSS 和 AI API；未设置 `OPENAI_API_KEY` 时分析会失败
- 可通过 `curl http://localhost:8082/items` 查看最新过滤结果
- 邮件功能可选，未配置 SMTP 时服务正常运行但不会发邮件