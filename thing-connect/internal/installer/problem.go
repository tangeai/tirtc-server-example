package installer

import (
	"errors"
	"strings"
)

// Explain returns the stable, customer-safe problem attached to setup HTTP
// failures and persisted installation state. Raw dependency errors remain in
// the wrapped cause and must only be written to protected server logs.
func Explain(err error) Problem {
	switch {
	case errors.Is(err, ErrMySQLUnavailable):
		return Problem{
			Code:    "MYSQL_UNAVAILABLE",
			Message: "MySQL 连接检查失败",
			Suggestions: []string{
				"确认 MySQL 地址和端口可从安装服务器访问",
				"检查 TLS 模式、迁移账号密码，以及 MySQL 对安装服务器来源地址的授权",
			},
		}
	case errors.Is(err, ErrRedisUnavailable):
		return Problem{
			Code:    "REDIS_UNAVAILABLE",
			Message: "Redis 连接检查失败",
			Suggestions: []string{
				"确认 Redis 地址和端口可从安装服务器访问",
				"检查密码、DB 编号、防火墙和 Redis 监听地址后重新预检",
			},
		}
	case errors.Is(err, ErrMQTTUnavailable):
		return Problem{
			Code:    "MQTT_UNAVAILABLE",
			Message: "MQTT 连接或认证失败",
			Suggestions: []string{
				"确认 Broker 地址使用 mqtt:// 或 mqtts://，且安装服务器可以访问对应端口",
				"检查认证方式、用户名或 ClientID、密码、TLS 证书链和 Broker 来源限制",
			},
		}
	case errors.Is(err, ErrUnknownDatabase):
		return Problem{
			Code:    "DATABASE_UNKNOWN_NONEMPTY",
			Message: "目标数据库非空且无法确认属于 ThingConnect，未执行任何写入",
			Suggestions: []string{
				"改用空数据库，或先备份并由数据库管理员确认现有表的来源",
				"不要删除未知表或手工伪造迁移记录后重试",
			},
		}
	case errors.Is(err, ErrExistingDatabase):
		return Problem{
			Code:    "DATABASE_ALREADY_IN_USE",
			Message: "目标数据库已有表，首次安装未执行任何写入",
			Suggestions: []string{
				"不要使用首次安装入口处理已有数据库；请先完成可验证备份，再按版本升级或恢复流程操作",
				"如需全新安装，请填写一个不存在或确认完全为空的专用数据库",
			},
		}
	case errors.Is(err, ErrSchemaFuture):
		return Problem{
			Code:    "SCHEMA_NEWER_THAN_BINARY",
			Message: "数据库版本高于当前程序",
			Suggestions: []string{
				"使用与该数据库相同或更高版本的 ThingConnect 安装包",
				"不要手工回退 schema_migrations 或删除已有表",
			},
		}
	case errors.Is(err, ErrSchemaDrift):
		return Problem{
			Code:    "SCHEMA_DRIFT",
			Message: "数据库结构与迁移记录不一致，已停止自动处理",
			Suggestions: []string{
				"保留现场并备份数据库，由管理员核对迁移记录和实际表结构",
				"修复结构漂移前不要反复执行安装或手工覆盖迁移版本",
			},
		}
	case errors.Is(err, ErrPlanStale):
		return Problem{
			Code:        "PLAN_STALE",
			Message:     "数据库状态或安装计划已经变化",
			Suggestions: []string{"返回连接信息页面重新执行预检，确认新计划后再继续"},
		}
	case errors.Is(err, ErrInstallBusy):
		return Problem{
			Code:        "INSTALL_BUSY",
			Message:     "另一个安装或发布任务正在运行",
			Suggestions: []string{"等待当前任务结束后刷新状态，不要在多个窗口同时提交安装"},
		}
	case errors.Is(err, ErrAlreadyInstalled):
		return Problem{
			Code:        "ALREADY_INSTALLED",
			Message:     "首次安装入口已永久关闭",
			Suggestions: []string{"从管理后台进行日常配置；恢复安装只能在服务器本地按部署流程执行"},
		}
	case errors.Is(err, ErrUnauthorized):
		return Problem{
			Code:        "SETUP_TOKEN_INVALID",
			Message:     "安装令牌无效",
			Suggestions: []string{"使用安装命令终端最后一次输出的一次性令牌；不要使用已轮换的旧令牌"},
		}
	case errors.Is(err, ErrInvalidOrigin):
		return Problem{
			Code:    "SETUP_ORIGIN_INVALID",
			Message: "安装请求来源无效",
			Suggestions: []string{
				"从当前安装服务器提供的 Admin 页面提交，不要跨域调用安装接口",
				"检查反向代理是否保留了正确的 Host 和 Origin",
			},
		}
	case errors.Is(err, ErrTooManyAttempts):
		return Problem{
			Code:        "SETUP_RATE_LIMITED",
			Message:     "安装验证请求过于频繁",
			Suggestions: []string{"等待一分钟后再试，并避免多个窗口或自动化脚本同时重复提交"},
		}
	case errors.Is(err, ErrInvalidInput):
		message := strings.TrimSpace(strings.TrimPrefix(err.Error(), ErrInvalidInput.Error()+":"))
		if message == "" || message == ErrInvalidInput.Error() {
			message = "安装信息校验失败"
		}
		return Problem{
			Code:        "INVALID_INPUT",
			Message:     message,
			Suggestions: []string{"检查页面标出的必填项、地址格式、端口范围和账号约束后重新预检"},
		}
	default:
		return Problem{
			Code:    "INSTALL_FAILED",
			Message: "安装未完成",
			Suggestions: []string{
				"保留当前页面和安装状态，检查 Admin 服务脱敏日志中的同一时间点错误",
				"修复原因后返回此页重试；不要删除数据库表或安装状态文件",
			},
		}
	}
}
