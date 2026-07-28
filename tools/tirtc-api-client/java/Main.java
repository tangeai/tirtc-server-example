import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.Map;

/**
 * tirtc-api-client: 服务端调用探鸽云 OpenAPI 示例
 *
 * javac TirtcSigning.java Main.java && java Main wxvoip
 */
public class Main {
    private static final String ENDPOINT_TOKEN = "https://api-tirtc.tange365.com";
    private static final String ENDPOINT_OPENAPI = "https://openapi-cn01.tange365.com";

    public static void main(String[] args) throws Exception {
        if (args.length < 1) {
            System.err.println("用法: java Main <api>");
            System.err.println("  wxvoip  — POST /v1/token/wxvoip");
            System.err.println("  aichat  — POST /v1/token/aichat");
            System.err.println("  login   — POST /v2/user/login/user-id");
            System.err.println("  plans   — GET  /v2/cloud-service/plans");
            System.exit(1);
        }

        String appId = requireEnv("TIRTC_APP_ID");
        String accessKey = requireEnv("TIRTC_ACCESS_KEY");
        String secretKey = requireEnv("TIRTC_SECRET_KEY");

        switch (args[0]) {
            case "wxvoip": runWxVoip(appId, accessKey, secretKey); break;
            case "aichat": runAiChat(appId, accessKey, secretKey); break;
            case "login":  runLogin(appId, accessKey, secretKey); break;
            case "plans":  runPlans(appId, accessKey, secretKey); break;
            default:
                System.err.println("未知 API: " + args[0]);
                System.exit(1);
        }
    }

    static void runWxVoip(String appId, String accessKey, String secretKey) throws Exception {
        String body = "{\"device_id\":\"TESTDEVICE01\",\"wx_session_key\":\"test-session-key\"," +
            "\"wx_room_id\":\"test-room-001\",\"wx_session_token\":\"test-server-token\"," +
            "\"wx_app_id\":\"wx0123456789abcdef\",\"wx_model_id\":\"model-001\"," +
            "\"audio_rate\":8000,\"audio_channels\":1}";
        System.out.println("=== POST /v1/token/wxvoip (微信 VoIP) ===");
        System.out.println("device_id: TESTDEVICE01\n");
        ApiResult r = doPost(appId, accessKey, secretKey, ENDPOINT_TOKEN, "/v1/token/wxvoip", body);
        printResult(r);
    }

    static void runAiChat(String appId, String accessKey, String secretKey) throws Exception {
        String body = "{\"device_id\":\"TESTDEVICE01\",\"role_id\":\"your-role-id\"}";
        System.out.println("=== POST /v1/token/aichat (AI 语音对话) ===");
        System.out.println("device_id: TESTDEVICE01\nrole_id:   your-role-id\n");
        ApiResult r = doPost(appId, accessKey, secretKey, ENDPOINT_TOKEN, "/v1/token/aichat", body);
        printResult(r);
    }

    static void runLogin(String appId, String accessKey, String secretKey) throws Exception {
        String body = "{\"user_id\":\"test-user-001\"}";
        System.out.println("=== POST /v2/user/login/user-id (用户登录) ===");
        System.out.println("user_id: test-user-001\n");
        ApiResult r = doPost(appId, accessKey, secretKey, ENDPOINT_OPENAPI, "/v2/user/login/user-id", body);
        printResult(r);
    }

    static void runPlans(String appId, String accessKey, String secretKey) throws Exception {
        System.out.println("=== GET /v2/cloud-service/plans (套餐列表) ===\n");
        ApiResult r = doGet(appId, accessKey, secretKey, ENDPOINT_OPENAPI, "/v2/cloud-service/plans", "");
        printResult(r);
    }

    static class ApiResult {
        final int statusCode;
        final String body;
        ApiResult(int statusCode, String body) { this.statusCode = statusCode; this.body = body; }
    }

    static ApiResult doPost(String appId, String accessKey, String secretKey,
                            String endpoint, String uriPath, String body) throws Exception {
        return doRequest(appId, accessKey, secretKey, endpoint, "POST", uriPath, "", body);
    }

    static ApiResult doGet(String appId, String accessKey, String secretKey,
                           String endpoint, String uriPath, String rawQuery) throws Exception {
        return doRequest(appId, accessKey, secretKey, endpoint, "GET", uriPath, rawQuery, null);
    }

    static ApiResult doRequest(String appId, String accessKey, String secretKey,
                               String endpoint, String method, String uriPath,
                               String rawQuery, String body) throws Exception {
        Map<String, String> headers = TirtcSigning.signRequest(
            accessKey, secretKey, appId, method, uriPath, rawQuery, body, null);

        String ep = System.getenv("TIRTC_ENDPOINT");
        if (ep == null || ep.isEmpty()) ep = endpoint;
        String fullUrl = ep + uriPath;
        if (rawQuery != null && !rawQuery.isEmpty()) fullUrl += "?" + rawQuery;
        System.out.println("→ " + method + " " + fullUrl);

        HttpRequest.Builder builder = HttpRequest.newBuilder()
            .uri(URI.create(fullUrl))
            .timeout(Duration.ofSeconds(15));
        for (Map.Entry<String, String> e : headers.entrySet()) {
            builder.header(e.getKey(), e.getValue());
        }
        if (body != null) {
            builder.method(method, HttpRequest.BodyPublishers.ofString(body));
        } else {
            builder.method(method, HttpRequest.BodyPublishers.noBody());
        }

        try {
            HttpClient client = HttpClient.newBuilder().build();
            HttpResponse<String> resp = client.send(builder.build(), HttpResponse.BodyHandlers.ofString());
            return new ApiResult(resp.statusCode(), resp.body());
        } catch (Exception e) {
            System.err.println("❌ 请求失败: " + e.getMessage());
            return new ApiResult(0, "");
        }
    }

    static void printResult(ApiResult r) {
        System.out.println("HTTP " + r.statusCode);
        if (!r.body.isEmpty()) {
            System.out.println(r.body);
            String json = r.body;
            int i = json.indexOf("\"code\"");
            if (i >= 0) {
                i = json.indexOf(":", i) + 1;
                while (i < json.length() && Character.isWhitespace(json.charAt(i))) i++;
                int j = i;
                while (j < json.length() && (Character.isDigit(json.charAt(j)) || json.charAt(j) == '-')) j++;
                int code = Integer.parseInt(json.substring(i, j));
                if (code == 0 || code == 200) System.out.println("✅ 成功");
                else if (code == 401 || code == 40105) System.out.println("❌ 签名验证失败");
                else System.out.println("⚠️  code=" + code);
            }
        }
    }

    static String requireEnv(String key) {
        String v = System.getenv(key);
        if (v == null || v.isEmpty()) {
            System.err.println("缺少环境变量 " + key);
            System.exit(1);
        }
        return v;
    }
}
