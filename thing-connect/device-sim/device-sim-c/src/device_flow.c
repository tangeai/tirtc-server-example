/** \file device_flow.c
 * \brief Device provisioning — HTTP + MQTT + mbedTLS signing.
 *
 * Embedded-reference: demonstrates how a device communicates with
 * the server using standard embedded-compatible C libraries.
 *
 * Libraries used:
 *   - libcurl       (HTTP GET/POST, TLS)
 *   - libmosquitto  (MQTT 3.1.1 client, TLS)
 *   - mbedTLS       (HMAC-SHA256, Base64)
 *   - pthread       (heartbeat thread, temp-mqtt wait)
 */

#include "device_flow.h"

#include <assert.h>
#include <signal.h>

#include <curl/curl.h>
#include <mosquitto.h>
#include <mbedtls/base64.h>
#include <mbedtls/md.h>

#include <cjson/cJSON.h>

#include "common.h"
#include "http_tls.h"

/* ── Global stop flag (shared across modules, defined in main.c) ──── */
extern volatile sig_atomic_t g_stop;

/* ── CA certificate path (set by main) ─────────────────────────────── */
static char g_ca_cert[512] = "../assets/ca-certificates.crt";
static int g_mqtt_insecure;

void set_mqtt_ca_cert(const char *path) {
    if (path && path[0]) {
        STR_COPY(g_ca_cert, path);
    }
}

void set_mqtt_insecure(int insecure) {
    g_mqtt_insecure = insecure ? 1 : 0;
}

/* Configure broker TLS once and fail fast on a missing/unreadable CA bundle.
 * Continuing without this check leaves a TLS broker unreachable while the UI
 * only reports the device as offline. */
static int _configure_mqtt_tls(struct mosquitto *mq, int use_tls) {
    if (!use_tls) return 0;
    if (access(g_ca_cert, R_OK) != 0) {
        LOG_E("MQTT CA 证书不可读: %s (%s)", g_ca_cert, strerror(errno));
        return -1;
    }
    int rc = mosquitto_tls_set(mq, g_ca_cert, NULL, NULL, NULL, NULL);
    if (rc != MOSQ_ERR_SUCCESS) {
        LOG_E("mosquitto_tls_set 失败: %s (cert=%s)", mosquitto_strerror(rc), g_ca_cert);
        return -1;
    }
    if (g_mqtt_insecure) {
        rc = mosquitto_tls_insecure_set(mq, true);
        if (rc != MOSQ_ERR_SUCCESS) {
            LOG_E("mosquitto_tls_insecure_set 失败: %s", mosquitto_strerror(rc));
            return -1;
        }
    }
    return 0;
}

/* =========================================================================
 *  HTTP helpers (libcurl)
 * ========================================================================= */

/** Write callback: append received data to a StrBuf. */
static size_t _write_cb(void *ptr, size_t size, size_t nmemb, void *user) {
    StrBuf *sb = (StrBuf *)user;
    size_t total = size * nmemb;
    if (sb->len + total >= sb->cap) {
        LOG_E("HTTP 响应过大 (%zu + %zu >= %zu)", sb->len, total, sb->cap);
        return 0;  /* signal error to libcurl */
    }
    memcpy(sb->buf + sb->len, ptr, total);
    sb->len += total;
    sb->buf[sb->len] = '\0';
    return total;
}

/** Perform a synchronous HTTP GET, store body in `body`. */
static int http_get(const char *url, StrBuf *body, long *http_code) {
    CURL *curl = curl_easy_init();
    if (!curl) return -1;

    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 10L);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, _write_cb);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, body);
    http_tls_apply(curl);

    CURLcode res = curl_easy_perform(curl);
    if (res != CURLE_OK) {
        LOG_E("HTTP GET %s 失败: %s", url, curl_easy_strerror(res));
        curl_easy_cleanup(curl);
        return -1;
    }
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, http_code);
    curl_easy_cleanup(curl);
    return 0;
}

/** Perform a synchronous HTTP POST with JSON body and optional headers.
 *  response body stored in `body`. */
static int http_post(const char *url, const char *json_body,
                     const char **headers, int nheaders,
                     StrBuf *body, long *http_code) {
    CURL *curl = curl_easy_init();
    if (!curl) return -1;

    struct curl_slist *hlist = NULL;
    if (headers) {
        for (int i = 0; i < nheaders; i++)
            hlist = curl_slist_append(hlist, headers[i]);
    }
    hlist = curl_slist_append(hlist, "Content-Type: application/json");

    curl_easy_setopt(curl, CURLOPT_URL, url);
    curl_easy_setopt(curl, CURLOPT_TIMEOUT, 10L);
    curl_easy_setopt(curl, CURLOPT_POSTFIELDS, json_body);
    curl_easy_setopt(curl, CURLOPT_HTTPHEADER, hlist);
    curl_easy_setopt(curl, CURLOPT_WRITEFUNCTION, _write_cb);
    curl_easy_setopt(curl, CURLOPT_WRITEDATA, body);
    http_tls_apply(curl);

    CURLcode res = curl_easy_perform(curl);
    curl_slist_free_all(hlist);
    if (res != CURLE_OK) {
        LOG_E("HTTP POST %s 失败: %s", url, curl_easy_strerror(res));
        curl_easy_cleanup(curl);
        return -1;
    }
    curl_easy_getinfo(curl, CURLINFO_RESPONSE_CODE, http_code);
    curl_easy_cleanup(curl);
    return 0;
}

/** Helper: extract code field from JSON response, return as int.
 *  Returns -1 if not found. */
static int json_code(const char *json_str) {
    cJSON *root = cJSON_Parse(json_str);
    if (!root) return -1;
    cJSON *code = cJSON_GetObjectItem(root, "code");
    int val = code && cJSON_IsNumber(code) ? code->valueint : -1;
    cJSON_Delete(root);
    return val;
}

/** Helper: extract a string field from the root JSON object. */
static int json_root_str(const char *json_str, const char *key,
                         char *out, size_t out_size) {
    cJSON *root = cJSON_Parse(json_str);
    if (!root) return -1;
    cJSON *item = cJSON_GetObjectItem(root, key);
    int ret = -1;
    if (item && cJSON_IsString(item) && item->valuestring[0] != '\0') {
        str_copy(out, out_size, item->valuestring);
        ret = 0;
    }
    cJSON_Delete(root);
    return ret;
}

/** Helper: extract data object from JSON response, return as string.
 *  Writes into out buffer. Returns 0 on success, -1 on error. */
static int json_data_str(const char *json_str, const char *key,
                         char *out, size_t out_size) {
    cJSON *root = cJSON_Parse(json_str);
    if (!root) return -1;
    cJSON *data = cJSON_GetObjectItem(root, "data");
    int ret = -1;
    if (data) {
        cJSON *item = cJSON_GetObjectItem(data, key);
        if (item && cJSON_IsString(item)) {
            str_copy(out, out_size, item->valuestring);
            ret = 0;
        }
    }
    cJSON_Delete(root);
    return ret;
}

/* =========================================================================
 *  Crypto: HMAC-SHA256 + Base64 (OpenSSL — 嵌入式替换为 mbedTLS 见 README)
 * ========================================================================= */

int hmac_sha256_b64(const char *key, const char *data,
                    char *out, size_t out_size) {
    unsigned char digest[32];
    int rc = mbedtls_md_hmac(mbedtls_md_info_from_type(MBEDTLS_MD_SHA256),
                             (const unsigned char *)key, strlen(key),
                             (const unsigned char *)data, strlen(data), digest);
    if (rc != 0) { LOG_E("HMAC-SHA256 签名失败: -0x%04x", -rc); return -1; }

    size_t olen = 0;
    rc = mbedtls_base64_encode((unsigned char *)out, out_size, &olen, digest, sizeof(digest));
    if (rc != 0 || olen >= out_size) { LOG_E("Base64 编码失败: -0x%04x", -rc); return -1; }
    out[olen] = '\0';
    return 0;
}

/* =========================================================================
 *  Service discovery
 * ========================================================================= */

int fetch_services(DeviceServices *svc, const char *base_url) {
    if (!svc) return -1;
    memset(svc, 0, sizeof(*svc));
    char url[512];
    if (base_url && base_url[0]) {
        snprintf(url, sizeof(url), "%s/services", base_url);
    } else {
        snprintf(url, sizeof(url), "https://ep-open.tangeopen.com/services");
    }
    LOG_D("fetch_services  GET %s", url);

    char body_buf[4096];
    StrBuf body;
    sb_init(&body, body_buf, sizeof(body_buf));

    long http_code = 0;
    if (http_get(url, &body, &http_code) != 0 || http_code != 200) {
        LOG_E("服务发现失败 HTTP %ld", http_code);
        return -1;
    }

    cJSON *root = cJSON_Parse(body.buf);
    if (!root) {
        LOG_E("服务发现响应非有效 JSON");
        return -1;
    }

    /* Extract each field */
    const char *fields[] = {"device-srv", "voip-srv", "ai-srv", "call-srv", "mqtt-srv"};
    for (int i = 0; i < 5; i++) {
        cJSON *item = cJSON_GetObjectItem(root, fields[i]);
        if (!item || !cJSON_IsString(item)) {
            if (i == 3) {  /* call-srv is optional */
                svc->call_server[0] = '\0';
                continue;
            }
            LOG_E("服务发现缺失字段: %s", fields[i]);
            cJSON_Delete(root);
            return -1;
        }
        const char *val = item->valuestring;
        if (i == 0) STR_COPY(svc->device_server, val);
        if (i == 1) STR_COPY(svc->voip_server, val);
        if (i == 2) STR_COPY(svc->ai_server, val);
        if (i == 3) STR_COPY(svc->call_server, val);
        if (i == 4) {
            /* Parse mqtt[s]://host:port */
            const char *hp = val;
            int is_tls = 0;
            if (strncmp(hp, "mqtts://", 8) == 0) { hp += 8; is_tls = 1; }
            else if (strncmp(hp, "mqtt://", 7) == 0) { hp += 7; is_tls = 0; }
            const char *colon = strrchr(hp, ':');
            if (!colon) {
                LOG_E("mqtt-srv 格式异常: %s", val);
                cJSON_Delete(root);
                return -1;
            }
            size_t host_len = (size_t)(colon - hp);
            if (host_len >= sizeof(svc->mqtt_host)) host_len = sizeof(svc->mqtt_host) - 1;
            memcpy(svc->mqtt_host, hp, host_len);
            svc->mqtt_host[host_len] = '\0';
            svc->mqtt_port = atoi(colon + 1);
            svc->mqtt_tls  = is_tls;
        }
    }

    /* tirtc-srv is optional — fall back to hardcoded default if missing */
    cJSON *tirtc = cJSON_GetObjectItem(root, "tirtc-srv");
    if (tirtc && cJSON_IsString(tirtc)) {
        STR_COPY(svc->tirtc_endpoint, tirtc->valuestring);
    } else {
        svc->tirtc_endpoint[0] = '\0';
    }

    cJSON_Delete(root);

    LOG_I("服务发现完成: device=%s mqtt=%s:%d tirtc=%s",
          svc->device_server, svc->mqtt_host, svc->mqtt_port,
          svc->tirtc_endpoint[0] ? svc->tirtc_endpoint : "(default)");
    return 0;
}

/* =========================================================================
 *  Device report
 * ========================================================================= */

int report_device(const char *server,
                  const char *mac, const char *device_id,
                  const char *device_key,
                  ReportResult *result) {
    char url[512];
    snprintf(url, sizeof(url), "%s/v1/device/report", server);

    char payload[512];
    cJSON *root = cJSON_CreateObject();
    if (!root) { LOG_E("设备上报 JSON 分配失败"); return -1; }
    if (!cJSON_AddStringToObject(root, "mac", mac ? mac : "")) {
        cJSON_Delete(root);
        LOG_E("设备上报 JSON 字段分配失败");
        return -1;
    }

    int n_hdrs = 0;
    const char *hdrs[4];
    char h_id[320], h_ts[320], h_nonce[320], h_sig[320];

    if (device_key && device_key[0]) {
        /* Signed report (scenario 1): prove identity via HMAC signature.
         * device_id goes in X-Device-Id header, NOT in body. */
        char ts_str[32];
        snprintf(ts_str, sizeof(ts_str), "%ld", (long)time(NULL));

        char nonce[17];
        rand_hex(nonce, 8);

        char raw[320];
        snprintf(raw, sizeof(raw), "%s%s%s",
                 device_id ? device_id : "", ts_str, nonce);

        char sig[64];
        if (hmac_sha256_b64(device_key, raw, sig, sizeof(sig)) != 0) {
            LOG_E("HMAC-SHA256 签名失败");
            cJSON_Delete(root);
            return -1;
        }

        snprintf(h_id,   sizeof(h_id),   "X-Device-Id: %s", device_id);
        snprintf(h_ts,   sizeof(h_ts),   "X-Timestamp: %s",  ts_str);
        snprintf(h_nonce, sizeof(h_nonce), "X-Nonce: %s",     nonce);
        snprintf(h_sig,  sizeof(h_sig),  "X-Signature: %s",   sig);

        hdrs[0] = h_id;  hdrs[1] = h_ts;
        hdrs[2] = h_nonce; hdrs[3] = h_sig;
        n_hdrs = 4;
        LOG_D("step 1  POST %s (signed) device_id=%s", url, device_id);
    } else if (device_id && device_id[0]) {
        /* Scenario 2 but caller passed device_id without key —
         * don't put it in body or it'll be rejected (scenario 3, 6014). */
        LOG_D("step 1  POST %s payload (device_key not provided; device_id omitted)", url);
    } else {
        LOG_D("step 1  POST %s payload=%s", url, "...");
    }

    char *json_str = cJSON_PrintUnformatted(root);
    cJSON_Delete(root);
    if (!json_str) { LOG_E("设备上报 JSON 序列化失败"); return -1; }
    STR_COPY(payload, json_str);
    free(json_str);

    char body_buf[4096];
    StrBuf body;
    sb_init(&body, body_buf, sizeof(body_buf));

    long http_code = 0;
    if (http_post(url, payload, hdrs, n_hdrs, &body, &http_code) != 0) {
        LOG_E("report_device HTTP 请求失败");
        return -1;
    }

    LOG_D("step 1  response HTTP:%ld  body:%.200s", http_code, body.buf);

    int code = json_code(body.buf);
    if (code == 429 || http_code == 429) {
        LOG_E("请求频率限制 (429)，请稍后重试");
        return -1;
    }
    if (code == 40901) {
        LOG_E("上一验证码仍有效 (40901)，请等待");
        return -1;
    }
    if (code == 6014) {
        LOG_E("设备ID不可信 (6014) — 需提供 device_key 以启用签名上报");
        return -1;
    }
    if (code != 200) {
        /* Try to get msg from response */
        cJSON *r = cJSON_Parse(body.buf);
        cJSON *msg = r ? cJSON_GetObjectItem(r, "msg") : NULL;
        LOG_E("设备上报失败 code=%d msg=%s", code,
              (msg && cJSON_IsString(msg)) ? msg->valuestring : "?");
        cJSON_Delete(r);
        return -1;
    }

    /* Extract data fields */
    memset(result, 0, sizeof(*result));
    json_data_str(body.buf, "code",            result->code, sizeof(result->code));
    json_data_str(body.buf, "temp_token",      result->temp_token, sizeof(result->temp_token));
    json_data_str(body.buf, "temp_client_id",  result->temp_client_id, sizeof(result->temp_client_id));

    if (!result->temp_client_id[0]) {
        LOG_E("服务端未返回 temp_client_id，请升级 device-server");
        return -1;
    }
    LOG_I("Verification code: %s", result->code);
    LOG_D("临时 MQTT 凭证已获取（敏感内容已隐藏）");
    LOG_I("temp_client  : %s", result->temp_client_id);
    return 0;
}

/* =========================================================================
 *  Token exchange
 * ========================================================================= */

int get_mqtt_token(const char *server,
                   const char *device_id, const char *device_key,
                   const char *mac,
                   char *token_out, size_t token_size) {
    char url[512];
    snprintf(url, sizeof(url), "%s/v1/device/token", server);

    /* Build signature */
    char ts_str[32];
    snprintf(ts_str, sizeof(ts_str), "%ld", (long)time(NULL));

    char nonce[17];
    rand_hex(nonce, 8);

    char raw[320];
    snprintf(raw, sizeof(raw), "%s%s%s", device_id, ts_str, nonce);

    char sig[64];
    if (hmac_sha256_b64(device_key, raw, sig, sizeof(sig)) != 0) {
        LOG_E("HMAC-SHA256 签名失败");
        return -1;
    }

    /* Build headers */
    char h_id[320], h_ts[320], h_nonce[320], h_mac[320], h_sig[320];
    snprintf(h_id,   sizeof(h_id),   "X-Device-Id: %s", device_id);
    snprintf(h_ts,   sizeof(h_ts),   "X-Timestamp: %s", ts_str);
    snprintf(h_nonce, sizeof(h_nonce),"X-Nonce: %s", nonce);
    snprintf(h_mac,  sizeof(h_mac),  "X-Mac: %s", mac ? mac : "");
    snprintf(h_sig,  sizeof(h_sig),  "X-Signature: %s", sig);

    const char *hdrs[] = { h_id, h_ts, h_nonce, h_mac, h_sig };
    LOG_D("step 1/4  POST %s  headers={X-Device-Id:%s X-Timestamp:%s X-Nonce:%s X-Signature:%s}",
          url, device_id, ts_str, nonce, sig);

    char body_buf[4096];
    StrBuf body;
    sb_init(&body, body_buf, sizeof(body_buf));

    long http_code = 0;
    if (http_post(url, "", hdrs, 5, &body, &http_code) != 0) {
        LOG_E("get_mqtt_token HTTP 请求失败");
        return -1;
    }

    LOG_D("step 1/4  response HTTP:%ld code=%d", http_code, json_code(body.buf));

    int code = json_code(body.buf);
    if (code == 6006) {
        LOG_W("设备已被解绑 (6006)，需重新通过验证码绑定");
        return -2;  /* special: caller should restart unbound flow */
    }
    if (code != 200) {
        char server_msg[512] = {0};
        const char *msg = json_root_str(body.buf, "msg", server_msg,
                                        sizeof(server_msg)) == 0
                              ? server_msg
                              : "服务器未返回错误说明";
        LOG_E("get_mqtt_token 失败 code=%d: %s", code, msg);
        return -1;
    }

    json_data_str(body.buf, "mqtt_token", token_out, token_size);
    LOG_I("mqtt_token 获取成功 (有效期 7 天)");
    LOG_D("MQTT 凭证已获取（敏感内容已隐藏）");
    return 0;
}

/* =========================================================================
 *  Temporary MQTT (wait for auth_grant)
 * ========================================================================= */

/** Shared state between main thread and mosquitto callbacks for temp MQTT. */
typedef struct {
    char            client_id[64];
    char            device_id[64];
    char            device_key[256];
    pthread_mutex_t mtx;
    pthread_cond_t  cond;
    int             done;
    int             connect_rc;
    int             ack_mid;      /* mid of the in-flight ack publish, -1 if none */
} TempMqttCtx;

static void _temp_on_connect(struct mosquitto *mq, void *obj, int rc) {
    TempMqttCtx *ctx = (TempMqttCtx *)obj;
    if (rc == 0) {
        LOG_I("step 2  临时 MQTT 连接成功");
        char topic[128];
        snprintf(topic, sizeof(topic), "device/%s/cmd", ctx->client_id);
        mosquitto_subscribe(mq, NULL, topic, 1);
        LOG_D("已订阅 %s，等待 auth_grant...", topic);
    } else {
        LOG_E("临时 MQTT 连接被拒绝 rc=%d (temp_token 已过期?)", rc);
        pthread_mutex_lock(&ctx->mtx);
        ctx->connect_rc = rc;
        ctx->done = 1;
        pthread_cond_signal(&ctx->cond);
        pthread_mutex_unlock(&ctx->mtx);
    }
}

/* Fires once the broker has PUBACK'd a QoS1 publish. Only the ack publish
 * sets ctx->ack_mid, so a match here means the ack has actually left the
 * device — safe to let the main thread disconnect. */
static void _temp_on_publish(struct mosquitto *mq, void *obj, int mid) {
    (void)mq;
    TempMqttCtx *ctx = (TempMqttCtx *)obj;
    pthread_mutex_lock(&ctx->mtx);
    if (ctx->ack_mid == mid) {
        ctx->done = 1;
        pthread_cond_signal(&ctx->cond);
    }
    pthread_mutex_unlock(&ctx->mtx);
}

static void _temp_on_message(struct mosquitto *mq, void *obj,
                             const struct mosquitto_message *msg) {
    TempMqttCtx *ctx = (TempMqttCtx *)obj;
    const char *raw = (const char *)msg->payload;
    LOG_D("MQTT received [%s]: %.*s", msg->topic, msg->payloadlen, raw);

    cJSON *root = cJSON_ParseWithLength(raw, (size_t)msg->payloadlen);
    if (!root) {
        LOG_E("MQTT JSON 解析失败: %.*s", msg->payloadlen, raw);
        return;
    }

    cJSON *type = cJSON_GetObjectItem(root, "type");
    if (!type || !cJSON_IsString(type)) {
        cJSON_Delete(root);
        return;
    }

    if (strcmp(type->valuestring, "auth_grant") == 0) {
        cJSON *payload = cJSON_GetObjectItem(root, "payload");
        if (payload && cJSON_IsObject(payload)) {
            cJSON *did = cJSON_GetObjectItem(payload, "device_id");
            cJSON *dkey = cJSON_GetObjectItem(payload, "device_key");
            if (did && cJSON_IsString(did) && dkey && cJSON_IsString(dkey)) {
                STR_COPY(ctx->device_id, did->valuestring);
                STR_COPY(ctx->device_key, dkey->valuestring);
                LOG_I("auth_grant 已收到: device_id=%s", ctx->device_id);
            } else {
                /* Pre-burned device: empty payload -> use local credentials */
                LOG_I("auth_grant 已收到 (预烧凭证设备)，使用本地凭证");
                ctx->device_id[0]  = '\0';
                ctx->device_key[0] = '\0';
            }
        } else {
            /* Pre-burned device: no payload field at all */
            LOG_I("auth_grant 已收到 (预烧凭证设备)，使用本地凭证");
            ctx->device_id[0]  = '\0';
            ctx->device_key[0] = '\0';
        }

        /* Send ACK. Completion is signaled from _temp_on_publish once the
         * broker has actually PUBACK'd this message (QoS1) — publishing only
         * queues the send; disconnecting right after mosquitto_publish()
         * races the network thread and can tear down the connection before
         * the ACK ever reaches the wire. */
        char ack_topic[128];
        int  ack_mid = 0;
        const char *ack = "{\"ack\":true}";
        snprintf(ack_topic, sizeof(ack_topic), "device/%s/ack", ctx->client_id);
        pthread_mutex_lock(&ctx->mtx);
        int publish_rc = mosquitto_publish(
            mq, &ack_mid, ack_topic, (int)strlen(ack), ack, 1, 0);
        if (publish_rc == MOSQ_ERR_SUCCESS) {
            ctx->ack_mid = ack_mid;
        } else {
            ctx->connect_rc = publish_rc;
            ctx->done = 1;
            pthread_cond_signal(&ctx->cond);
        }
        pthread_mutex_unlock(&ctx->mtx);
        if (publish_rc == MOSQ_ERR_SUCCESS)
            LOG_D("MQTT sent [%s]: %s", ack_topic, ack);
        else
            LOG_E("MQTT ACK 发布失败: %s", mosquitto_strerror(publish_rc));
    } else {
        LOG_E("未知消息类型: %s", type->valuestring);
    }
    cJSON_Delete(root);
}

int connect_temp_mqtt(const char *host, int port,
                      const char *temp_client_id, const char *temp_token,
                      int timeout_sec, int use_tls,
                      char *device_id_out, size_t id_size,
                      char *device_key_out, size_t key_size) {
    TempMqttCtx ctx;
    memset(&ctx, 0, sizeof(ctx));
    STR_COPY(ctx.client_id, temp_client_id);
    ctx.device_id[0]  = '\0';
    ctx.device_key[0] = '\0';
    ctx.done          = 0;
    ctx.connect_rc    = 0;
    ctx.ack_mid       = -1;  /* no ack published yet; mosquitto mids start at 1 */
    pthread_mutex_init(&ctx.mtx, NULL);
    pthread_cond_init(&ctx.cond, NULL);

    mosquitto_lib_init();

    struct mosquitto *mq = mosquitto_new(temp_client_id, true, &ctx);
    if (!mq) {
        LOG_E("mosquitto_new 失败");
        mosquitto_lib_cleanup();
        pthread_cond_destroy(&ctx.cond);
        pthread_mutex_destroy(&ctx.mtx);
        return -1;
    }

    mosquitto_int_option(mq, MOSQ_OPT_PROTOCOL_VERSION, MQTT_PROTOCOL_V311);
    mosquitto_username_pw_set(mq, temp_client_id, temp_token);
    if (_configure_mqtt_tls(mq, use_tls) != 0) {
        mosquitto_destroy(mq);
        mosquitto_lib_cleanup();
        pthread_mutex_destroy(&ctx.mtx);
        pthread_cond_destroy(&ctx.cond);
        return -1;
    }

    mosquitto_connect_callback_set(mq, _temp_on_connect);
    mosquitto_message_callback_set(mq, _temp_on_message);
    mosquitto_publish_callback_set(mq, _temp_on_publish);

    LOG_D("MQTT connect params: ClientID=%s Username=%s PasswordLen=%zu",
          temp_client_id, temp_client_id, strlen(temp_token));

    int rc = mosquitto_connect(mq, host, port, 60);
    if (rc != MOSQ_ERR_SUCCESS) {
        LOG_E("mosquitto_connect 失败: %s", mosquitto_strerror(rc));
        mosquitto_destroy(mq);
        mosquitto_lib_cleanup();
        pthread_cond_destroy(&ctx.cond);
        pthread_mutex_destroy(&ctx.mtx);
        return -1;
    }

    rc = mosquitto_loop_start(mq);
    if (rc != MOSQ_ERR_SUCCESS) {
        LOG_E("mosquitto_loop_start 失败: %s", mosquitto_strerror(rc));
        mosquitto_destroy(mq);
        mosquitto_lib_cleanup();
        pthread_cond_destroy(&ctx.cond);
        pthread_mutex_destroy(&ctx.mtx);
        return -1;
    }

    /* Wait for auth_grant */
    struct timespec deadline;
    clock_gettime(CLOCK_REALTIME, &deadline);
    deadline.tv_sec += timeout_sec;

    pthread_mutex_lock(&ctx.mtx);
    int timed_out = 0;
    while (!ctx.done && !timed_out && !g_stop) {
        struct timespec ts;
        clock_gettime(CLOCK_REALTIME, &ts);
        ts.tv_sec += 1;
        if (pthread_cond_timedwait(&ctx.cond, &ctx.mtx, &ts) == ETIMEDOUT) {
            struct timespec now;
            clock_gettime(CLOCK_REALTIME, &now);
            if (now.tv_sec >= deadline.tv_sec) timed_out = 1;
        }
    }
    pthread_mutex_unlock(&ctx.mtx);

    /* disconnect first so the network thread can exit cleanly, then join it */
    mosquitto_disconnect(mq);
    mosquitto_loop_stop(mq, true);  /* force stop — don't wait for blocked TLS socket */
    mosquitto_destroy(mq);
    mosquitto_lib_cleanup();

    pthread_mutex_destroy(&ctx.mtx);
    pthread_cond_destroy(&ctx.cond);

    if (timed_out || !ctx.done) {
        LOG_E("等待 auth_grant 超时 (%ds)", timeout_sec);
        return -1;
    }
    if (ctx.connect_rc != 0) {
        return -1;
    }

    /* Copy results back — only if auth_grant carried new credentials.
     * Pre-burned devices receive an empty-payload auth_grant meaning
     * "your existing credentials remain valid"; do not overwrite them. */
    if (device_id_out && ctx.device_id[0])
        str_copy(device_id_out, id_size, ctx.device_id);
    if (device_key_out && ctx.device_key[0])
        str_copy(device_key_out, key_size, ctx.device_key);
    return 0;
}

/* =========================================================================
 *  Permanent MQTT (long-lived connection)
 * ========================================================================= */

typedef struct {
    struct mosquitto   *mq;
    const MqttMsgHandler *handler;
    void                 *ctx;
    volatile sig_atomic_t *stop_flag;
    char                  device_id[64];
} PermMqttCtx;

static void _perm_on_connect(struct mosquitto *mq, void *obj, int rc) {
    PermMqttCtx *ctx = (PermMqttCtx *)obj;
    if (rc == 0) {
        LOG_I("step 2/4  永久 MQTT 连接成功  ClientID=sn_%s",
              ctx->device_id);

        char cmd_topic[128], notify_topic[128];
        snprintf(cmd_topic,    sizeof(cmd_topic),    "device/sn_%s/cmd",    ctx->device_id);
        snprintf(notify_topic, sizeof(notify_topic), "device/sn_%s/notify", ctx->device_id);
        mosquitto_subscribe(mq, NULL, cmd_topic, 1);
        mosquitto_subscribe(mq, NULL, notify_topic, 1);
        LOG_I("step 3/4  设备已上线，长连接已建立 (Ctrl+C 退出)");
    } else {
        LOG_E("永久 MQTT 连接被拒绝 rc=%d", rc);
        *ctx->stop_flag = 1;
    }
}

static void _perm_on_disconnect(struct mosquitto *mq, void *obj, int rc) {
    PermMqttCtx *ctx = (PermMqttCtx *)obj;
    (void)mq;
    if (*ctx->stop_flag) return;
    if (rc == 0x98 || rc == 0x99 || rc == 152 || rc == 153) {
        LOG_W("Token 已过期 (rc=%#x)，请重新获取 mqtt_token 并重连", rc);
    } else if (rc != 0) {
        LOG_W("MQTT 断开 rc=%d，自动重连中...", rc);
    }
}

static void _perm_on_message(struct mosquitto *mq, void *obj,
                             const struct mosquitto_message *msg) {
    PermMqttCtx *ctx = (PermMqttCtx *)obj;
    const char *raw = (const char *)msg->payload;
    LOG_D("MQTT message [%s]: %.*s", msg->topic, msg->payloadlen, raw);

    /* Auto-ACK for /cmd topics */
    if (strstr(msg->topic, "/cmd")) {
        char ack_topic[128];
        snprintf(ack_topic, sizeof(ack_topic), "device/sn_%s/ack", ctx->device_id);
        const char *ack = "{\"ack\":true}";
        mosquitto_publish(mq, NULL, ack_topic, (int)strlen(ack), ack, 1, 0);
        LOG_D("MQTT sent ACK [%s]", ack_topic);
    }

    cJSON *root = cJSON_ParseWithLength(raw, (size_t)msg->payloadlen);
    if (!root) {
        LOG_E("MQTT JSON parse failed: %.*s", msg->payloadlen, raw);
        return;
    }

    cJSON *type    = cJSON_GetObjectItem(root, "type");
    cJSON *channel = cJSON_GetObjectItem(root, "channel");
    cJSON *payload = cJSON_GetObjectItem(root, "payload");

    const char *t = type    && cJSON_IsString(type)    ? type->valuestring    : "";
    const char *ch = channel && cJSON_IsString(channel) ? channel->valuestring : "";

    if (strcmp(t, "unbind") == 0) {
        LOG_W("收到解绑通知，断开连接（凭证已保留）...");
        *ctx->stop_flag = 1;
    } else if (strcmp(t, "call_incoming") == 0 && strcmp(ch, "wx") == 0) {
        if (ctx->handler && ctx->handler->on_call_incoming)
            ctx->handler->on_call_incoming(ctx->ctx, payload);
    } else if (strcmp(t, "callers_update") == 0 && strcmp(ch, "wx") == 0) {
        if (ctx->handler && ctx->handler->on_callers_update)
            ctx->handler->on_callers_update(ctx->ctx);
    } else if (strcmp(t, "call_cancel") == 0 && strcmp(ch, "wx") == 0) {
        if (ctx->handler && ctx->handler->on_call_cancel)
            ctx->handler->on_call_cancel(ctx->ctx, payload);
    }

    /* channel=device (device-to-device P2P call) */
    if (strcmp(ch, "device") == 0) {
        if (strcmp(t, "call_incoming") == 0) {
            if (ctx->handler && ctx->handler->on_device_call_incoming)
                ctx->handler->on_device_call_incoming(ctx->ctx, payload);
        } else if (strcmp(t, "room_cancel") == 0) {
            if (ctx->handler && ctx->handler->on_device_room_cancel)
                ctx->handler->on_device_room_cancel(ctx->ctx, payload);
        } else if (strcmp(t, "call_reject") == 0) {
            if (ctx->handler && ctx->handler->on_device_call_reject)
                ctx->handler->on_device_call_reject(ctx->ctx, payload);
        } else if (strcmp(t, "callers_update") == 0) {
            if (ctx->handler && ctx->handler->on_device_callers_update_ex)
                ctx->handler->on_device_callers_update_ex(ctx->ctx, payload);
            else if (ctx->handler && ctx->handler->on_device_callers_update)
                ctx->handler->on_device_callers_update(ctx->ctx);
        } else if (strcmp(t, "callee_answered") == 0) {
            if (ctx->handler && ctx->handler->on_device_callee_answered)
                ctx->handler->on_device_callee_answered(ctx->ctx, payload);
        }
    }

    cJSON_Delete(root);
}

/** Heartbeat thread: publish every 30 seconds. */
static void *_heartbeat_thread(void *arg) {
    PermMqttCtx *ctx = (PermMqttCtx *)arg;
    int seq = 0;
    int ticks = 0;  /* count 1s ticks, publish every 30 */

    while (!*ctx->stop_flag) {
        sleep_ms(1000);
        if (*ctx->stop_flag) break;
        if (++ticks < 30) continue;
        ticks = 0;

        seq++;
        char hb_topic[128];
        snprintf(hb_topic, sizeof(hb_topic), "device/sn_%s/up", ctx->device_id);

        char payload[128];
        snprintf(payload, sizeof(payload),
                 "{\"type\":\"heartbeat\",\"seq\":%d,\"ts\":%ld}", seq, (long)time(NULL));

        mosquitto_publish(ctx->mq, NULL, hb_topic, (int)strlen(payload), payload, 0, 0);
        LOG_D("heartbeat seq=%d -> %s", seq, hb_topic);
    }
    return NULL;
}

int connect_mqtt_blocking(const char *host, int port,
                          const char *device_id, const char *mqtt_token,
                          const MqttMsgHandler *handler, void *ctx_ptr,
                          volatile sig_atomic_t *stop_flag, int use_tls) {
    char client_id[128];
    snprintf(client_id, sizeof(client_id), "sn_%s", device_id);

    PermMqttCtx ctx_obj;
    memset(&ctx_obj, 0, sizeof(ctx_obj));
    ctx_obj.handler   = handler;
    ctx_obj.ctx       = ctx_ptr;
    ctx_obj.stop_flag = stop_flag;
    STR_COPY(ctx_obj.device_id, device_id);

    mosquitto_lib_init();

    struct mosquitto *mq = mosquitto_new(client_id, true, &ctx_obj);
    if (!mq) {
        LOG_E("mosquitto_new 失败");
        mosquitto_lib_cleanup();
        return -1;
    }

    mosquitto_int_option(mq, MOSQ_OPT_PROTOCOL_VERSION, MQTT_PROTOCOL_V311);
    mosquitto_username_pw_set(mq, device_id, mqtt_token);
    if (_configure_mqtt_tls(mq, use_tls) != 0) {
        mosquitto_destroy(mq);
        mosquitto_lib_cleanup();
        return -1;
    }

    mosquitto_connect_callback_set(mq, _perm_on_connect);
    mosquitto_disconnect_callback_set(mq, _perm_on_disconnect);
    mosquitto_message_callback_set(mq, _perm_on_message);

    LOG_D("MQTT connect params: ClientID=%s Username=%s TLS=%d", client_id, device_id, use_tls);

    int rc = mosquitto_connect(mq, host, port, 60);
    if (rc != MOSQ_ERR_SUCCESS) {
        LOG_E("mosquitto_connect 失败: %s", mosquitto_strerror(rc));
        mosquitto_destroy(mq);
        mosquitto_lib_cleanup();
        return -1;
    }

    ctx_obj.mq = mq;
    rc = mosquitto_loop_start(mq);
    if (rc != MOSQ_ERR_SUCCESS) {
        LOG_E("mosquitto_loop_start 失败: %s", mosquitto_strerror(rc));
        mosquitto_destroy(mq);
        mosquitto_lib_cleanup();
        return -1;
    }

    /* Start heartbeat thread — joined before cleanup so it never outlives ctx_obj/mq */
    pthread_t hb_tid;
    if (pthread_create(&hb_tid, NULL, _heartbeat_thread, &ctx_obj) != 0) {
        LOG_E("无法创建 MQTT 心跳线程");
        mosquitto_loop_stop(mq, true);
        mosquitto_destroy(mq);
        mosquitto_lib_cleanup();
        return -1;
    }

    /* Block until stop */
    while (!*stop_flag)
        sleep_ms(500);

    pthread_join(hb_tid, NULL);  /* wait for heartbeat to exit before destroying mq */
    mosquitto_loop_stop(mq, true);  /* force stop — don't wait for blocked TLS socket */
    mosquitto_disconnect(mq);
    mosquitto_destroy(mq);
    mosquitto_lib_cleanup();

    LOG_I("MQTT 已断开");
    return 0;
}
