using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

namespace TirtcApiClient;

/// <summary>
/// TGV1-HMAC-SHA256 request signing for tange.ai server APIs.
/// </summary>
public static class TirtcSigning
{
    private const string AlgTgv1 = "TGV1-HMAC-SHA256";

    public static Dictionary<string, string> SignRequest(
        string accessKey, string accessSecret, string appId,
        string method, string uriPath, string rawQuery = "",
        string? body = null, DateTimeOffset? signingTime = null)
    {
        method = method.ToUpperInvariant();
        var now = signingTime ?? DateTimeOffset.UtcNow;
        var bodyStr = body ?? "";
        var payloadHash = Sha256Hex(bodyStr);

        // Step 1: build header map
        var hv = new Dictionary<string, string>
        {
            ["X-Tg-Algorithm"] = AlgTgv1,
            ["X-Tg-Date"] = now.ToString("yyyyMMdd'T'HHmmss'Z'"),
            ["X-Tg-App-Id"] = appId.Trim(),
            ["X-Tg-Content-Sha256"] = payloadHash,
        };
        // Signed headers: only x-tg-app-id, x-tg-date (+ content-type, content-length for body methods)
        var signedNames = new List<string> { "x-tg-app-id", "x-tg-date" };
        if (method is "POST" or "PUT" or "PATCH")
        {
            hv["Content-Type"] = "application/json";
            hv["Content-Length"] = Encoding.UTF8.GetByteCount(bodyStr).ToString();
            signedNames.Insert(0, "content-length");
            signedNames.Insert(0, "content-type");
        }

        // Step 2: signed header names string
        var signedHdrs = string.Join(";", signedNames);

        // Step 3: canonical headers string
        var lines = signedNames.Select(k => $"{k.ToLowerInvariant()}:{hv[k].Trim()}");
        var canonHdrs = string.Join("\n", lines);

        // Step 4: canonical request
        var uri = CanonicalUri(uriPath);
        var qCanon = CanonicalQuery(method, (rawQuery ?? "").TrimStart('?'));
        var canonReq = string.Join("\n", method, uri, qCanon, canonHdrs, signedHdrs, payloadHash);

        // Step 5: string to sign
        var scopeDate = now.AddDays(7).ToString("yyyyMMdd");
        var scope = $"{scopeDate}/tgv1_request";
        var strToSign = string.Join("\n", AlgTgv1, hv["X-Tg-Date"], scope, Sha256Hex(canonReq));

        // Step 6: derive signing key
        var k = HmacSha256(now.ToString("yyyyMMdd"), Encoding.UTF8.GetBytes("TGV1" + accessSecret));
        k = HmacSha256(uri, k);
        k = HmacSha256("tgv1_request", k);

        // Step 7: signature
        var sig = HmacSha256Hex(strToSign, k);

        // Step 8: Authorization
        var auth = $"{AlgTgv1} Credential={accessKey}/{scope}, SignedHeaders={signedHdrs}, Signature={sig}";

        var result = new Dictionary<string, string>
        {
            ["X-Tg-Algorithm"] = hv["X-Tg-Algorithm"],
            ["X-Tg-Date"] = hv["X-Tg-Date"],
            ["X-Tg-App-Id"] = hv["X-Tg-App-Id"],
            ["X-Tg-Content-Sha256"] = hv["X-Tg-Content-Sha256"],
            ["X-Tg-Signed-Headers"] = signedHdrs,
            ["Authorization"] = auth,
        };
        if (hv.ContainsKey("Content-Type")) result["Content-Type"] = hv["Content-Type"];
        if (hv.ContainsKey("Content-Length")) result["Content-Length"] = hv["Content-Length"];
        return result;
    }

    private static string Sha256Hex(string data)
    {
        var hash = SHA256.HashData(Encoding.UTF8.GetBytes(data));
        return Convert.ToHexStringLower(hash);
    }

    private static byte[] HmacSha256(string data, byte[] key)
    {
        using var hmac = new HMACSHA256(key);
        return hmac.ComputeHash(Encoding.UTF8.GetBytes(data));
    }

    private static string HmacSha256Hex(string data, byte[] key)
    {
        return Convert.ToHexStringLower(HmacSha256(data, key));
    }

    private static string CanonicalUri(string p)
    {
        p = p.Trim();
        if (p.Length > 1 && p.EndsWith('/'))
            p = p[..^1];
        return p;
    }

    private static string CanonicalQuery(string method, string rawQuery)
    {
        if (method.ToUpperInvariant() is "POST" or "PUT" or "PATCH")
            return "";
        return rawQuery.Replace("+", "%20");
    }
}

public class Program
{
    private const string EndpointToken = "https://api-tirtc.tange365.com";
    private const string EndpointOpenApi = "https://openapi-cn01.tange365.com";

    private static readonly HttpClient Client = new() { Timeout = TimeSpan.FromSeconds(15) };

    public static async Task<int> Main(string[] args)
    {
        if (args.Length < 1)
        {
            Console.Error.WriteLine("用法: dotnet run -- <api>");
            Console.Error.WriteLine("  wxvoip  — POST /v1/token/wxvoip");
            Console.Error.WriteLine("  aichat  — POST /v1/token/aichat");
            Console.Error.WriteLine("  login   — POST /v2/user/login/user-id");
            Console.Error.WriteLine("  plans   — GET  /v2/cloud-service/plans");
            return 1;
        }

        var appId = RequireEnv("TIRTC_APP_ID");
        var accessKey = RequireEnv("TIRTC_ACCESS_KEY");
        var secretKey = RequireEnv("TIRTC_SECRET_KEY");

        switch (args[0])
        {
            case "wxvoip": await RunWxVoip(appId, accessKey, secretKey); break;
            case "aichat": await RunAiChat(appId, accessKey, secretKey); break;
            case "login":  await RunLogin(appId, accessKey, secretKey); break;
            case "plans":  await RunPlans(appId, accessKey, secretKey); break;
            default:
                Console.Error.WriteLine($"未知 API: {args[0]}");
                return 1;
        }
        return 0;
    }

    static async Task RunWxVoip(string appId, string accessKey, string secretKey)
    {
        var body = JsonSerializer.Serialize(new
        {
            device_id = "TESTDEVICE01",
            wx_session_key = "test-session-key",
            wx_room_id = "test-room-001",
            wx_session_token = "test-server-token",
            wx_app_id = "wx0123456789abcdef",
            wx_model_id = "model-001",
            audio_rate = 8000,
            audio_channels = 1,
        });
        Console.WriteLine("=== POST /v1/token/wxvoip (微信 VoIP) ===");
        Console.WriteLine("device_id: TESTDEVICE01\n");
        var r = await DoPost(appId, accessKey, secretKey, EndpointToken, "/v1/token/wxvoip", body);
        PrintResult(r);
    }

    static async Task RunAiChat(string appId, string accessKey, string secretKey)
    {
        var body = JsonSerializer.Serialize(new { device_id = "TESTDEVICE01", role_id = "your-role-id" });
        Console.WriteLine("=== POST /v1/token/aichat (AI 语音对话) ===");
        Console.WriteLine("device_id: TESTDEVICE01\nrole_id:   your-role-id\n");
        var r = await DoPost(appId, accessKey, secretKey, EndpointToken, "/v1/token/aichat", body);
        PrintResult(r);
    }

    static async Task RunLogin(string appId, string accessKey, string secretKey)
    {
        var body = JsonSerializer.Serialize(new { user_id = "test-user-001" });
        Console.WriteLine("=== POST /v2/user/login/user-id (用户登录) ===");
        Console.WriteLine("user_id: test-user-001\n");
        var r = await DoPost(appId, accessKey, secretKey, EndpointOpenApi, "/v2/user/login/user-id", body);
        PrintResult(r);
    }

    static async Task RunPlans(string appId, string accessKey, string secretKey)
    {
        Console.WriteLine("=== GET /v2/cloud-service/plans (套餐列表) ===\n");
        var r = await DoGet(appId, accessKey, secretKey, EndpointOpenApi, "/v2/cloud-service/plans");
        PrintResult(r);
    }

    static async Task<(int StatusCode, string Body)> DoPost(string appId, string accessKey, string secretKey, string endpoint, string uriPath, string body)
    {
        return await DoRequest(appId, accessKey, secretKey, endpoint, "POST", uriPath, "", body);
    }

    static async Task<(int, string)> DoGet(string appId, string accessKey, string secretKey, string endpoint, string uriPath, string rawQuery = "")
    {
        return await DoRequest(appId, accessKey, secretKey, endpoint, "GET", uriPath, rawQuery, null);
    }

    static async Task<(int StatusCode, string Body)> DoRequest(string appId, string accessKey, string secretKey,
        string endpoint, string method, string uriPath, string rawQuery, string? body)
    {
        var headers = TirtcSigning.SignRequest(accessKey, secretKey, appId, method, uriPath, rawQuery, body);

        endpoint = Environment.GetEnvironmentVariable("TIRTC_ENDPOINT") ?? endpoint;
        var fullUrl = endpoint + uriPath;
        if (!string.IsNullOrEmpty(rawQuery)) fullUrl += "?" + rawQuery;
        Console.WriteLine($"→ {method} {fullUrl}");

        var req = new HttpRequestMessage(new HttpMethod(method), fullUrl);
        foreach (var kv in headers)
            req.Headers.TryAddWithoutValidation(kv.Key, kv.Value);
        if (body != null)
            req.Content = new StringContent(body, Encoding.UTF8, "application/json");

        try
        {
            var resp = await Client.SendAsync(req);
            var respBody = await resp.Content.ReadAsStringAsync();
            return ((int)resp.StatusCode, respBody);
        }
        catch (Exception e)
        {
            Console.Error.WriteLine($"❌ 请求失败: {e.Message}");
            return (0, "");
        }
    }

    static void PrintResult((int statusCode, string body) r)
    {
        Console.WriteLine($"HTTP {r.statusCode}");
        if (!string.IsNullOrEmpty(r.body))
        {
            try
            {
                var doc = JsonDocument.Parse(r.body);
                Console.WriteLine(JsonSerializer.Serialize(doc, new JsonSerializerOptions { WriteIndented = true }));
                var code = doc.RootElement.TryGetProperty("code", out var c) ? c.GetInt32() : -1;
                if (code == 0 || code == 200) Console.WriteLine("✅ 成功");
                else if (code == 401 || code == 40105) Console.WriteLine("❌ 签名验证失败");
                else Console.WriteLine($"⚠️  code={code}");
            }
            catch
            {
                Console.WriteLine(r.body);
            }
        }
    }

    static string RequireEnv(string key)
    {
        var v = Environment.GetEnvironmentVariable(key);
        if (string.IsNullOrEmpty(v))
        {
            Console.Error.WriteLine($"缺少环境变量 {key}");
            Environment.Exit(1);
        }
        return v!;
    }
}
