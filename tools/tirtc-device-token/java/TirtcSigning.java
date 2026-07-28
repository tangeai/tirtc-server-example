import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;
import java.time.OffsetDateTime;
import java.time.ZoneOffset;
import java.time.format.DateTimeFormatter;
import java.util.*;

/**
 * TGV1-HMAC-SHA256 request signing for tange.ai TiRTC server APIs.
 *
 * <p>Reference: https://tange-ai.feishu.cn/wiki/wikcn6GaETEIVO0jgCP1ASHf3Mg
 *
 * <p>Usage:
 * <pre>{@code
 * Map<String, String> headers = TirtcSigning.signRequest(
 *     "your-access-key", "your-access-secret", "your-app-id",
 *     "POST", "/v2/device/info", "{\"device_id\":\"TEST\"}", "", null
 * );
 * }</pre>
 */
public final class TirtcSigning {

    public static final String TGV1_ALG = "TGV1-HMAC-SHA256";
    private static final String CREDENTIAL_SCOPE_SUFFIX = "tgv1_request";
    private static final DateTimeFormatter FMT_DATE = DateTimeFormatter.ofPattern("yyyyMMdd");
    private static final DateTimeFormatter FMT_TG = DateTimeFormatter.ofPattern("yyyyMMdd'T'HHmmss'Z'");
    private static final Set<String> METHODS_WITH_BODY = Set.of("POST", "PUT", "PATCH");

    private TirtcSigning() {
        // utility class
    }

    /**
     * Build TGV1-HMAC-SHA256 signed HTTP headers.
     *
     * @param accessKey    appears in the Authorization Credential field
     * @param accessSecret used for HMAC key derivation (keep this secret)
     * @param appId        TiRTC application ID (X-Tg-App-Id header)
     * @param method       HTTP method (GET, POST, PUT, PATCH, DELETE)
     * @param uriPath      URI path, e.g. "/v2/device/info"
     * @param body         request body string (empty for GET/DELETE)
     * @param rawQuery     raw query without leading "?" (ignored for POST/PUT/PATCH)
     * @param signingTime  UTC time; uses now if null
     * @return unmodifiable map of HTTP header name to value
     */
    public static Map<String, String> signRequest(
            String accessKey,
            String accessSecret,
            String appId,
            String method,
            String uriPath,
            String body,
            String rawQuery,
            OffsetDateTime signingTime) {

        method = method.toUpperCase();
        if (signingTime == null) {
            signingTime = OffsetDateTime.now(ZoneOffset.UTC);
        }

        byte[] bodyBytes = body.getBytes(StandardCharsets.UTF_8);
        String tgDate = signingTime.format(FMT_TG);
        String scope = buildCredentialScope(signingTime);
        String payloadHash = sha256Hex(bodyBytes);

        // Step 1: build header values map
        Map<String, String> hv = new LinkedHashMap<>();
        hv.put("x-tg-algorithm", TGV1_ALG);
        hv.put("x-tg-date", tgDate);
        hv.put("x-tg-app-id", appId.trim());
        hv.put("x-tg-content-sha256", payloadHash);
        if (METHODS_WITH_BODY.contains(method)) {
            hv.put("content-type", "application/json");
            hv.put("content-length", String.valueOf(bodyBytes.length));
        }

        // Step 2: sorted lowercased header names
        List<String> names = new ArrayList<>();
        for (String k : hv.keySet()) {
            names.add(k.toLowerCase());
        }
        Collections.sort(names);
        String signedHeaders = String.join(";", names);

        // Step 3: canonical header string
        List<String> canonLines = new ArrayList<>();
        for (String name : names) {
            canonLines.add(name + ":" + hv.get(name).trim());
        }
        String hCanon = String.join("\n", canonLines);

        // Step 4: canonical request
        String uriP = canonicalURIPath(uriPath);
        String qCanon = canonicalQuery(method, rawQuery);
        String canonicalReq = String.join("\n",
                method, uriP, qCanon, hCanon, signedHeaders, payloadHash);

        // Step 5: string-to-sign
        String hashCanon = sha256Hex(canonicalReq.getBytes(StandardCharsets.UTF_8));
        String strToSign = String.join("\n",
                TGV1_ALG, tgDate, scope, hashCanon);

        // Step 6: derive signing key
        byte[] k = hmacSha256(signingTime.format(FMT_DATE), ("TGV1" + accessSecret).getBytes(StandardCharsets.UTF_8));
        k = hmacSha256(uriP, k);
        k = hmacSha256(CREDENTIAL_SCOPE_SUFFIX, k);

        // Step 7: compute signature
        String sig = hmacSha256Hex(strToSign, k);

        // Step 8: build Authorization header
        String cred = accessKey + "/" + scope;
        String auth = TGV1_ALG + " Credential=" + cred + ", SignedHeaders=" + signedHeaders + ", Signature=" + sig;

        // Build output
        Map<String, String> result = new LinkedHashMap<>();
        result.put("X-Tg-Algorithm", hv.get("x-tg-algorithm"));
        result.put("X-Tg-Date", hv.get("x-tg-date"));
        result.put("X-Tg-App-Id", hv.get("x-tg-app-id"));
        result.put("X-Tg-Content-Sha256", hv.get("x-tg-content-sha256"));
        result.put("X-Tg-Signed-Headers", signedHeaders);
        result.put("Authorization", auth);
        if (hv.containsKey("content-type")) {
            result.put("Content-Type", hv.get("content-type"));
        }
        if (hv.containsKey("content-length")) {
            result.put("Content-Length", hv.get("content-length"));
        }
        return Collections.unmodifiableMap(result);
    }

    // --- crypto helpers ---

    private static String sha256Hex(byte[] data) {
        try {
            MessageDigest md = MessageDigest.getInstance("SHA-256");
            byte[] digest = md.digest(data);
            StringBuilder sb = new StringBuilder();
            for (byte b : digest) {
                sb.append(String.format("%02x", b));
            }
            return sb.toString();
        } catch (NoSuchAlgorithmException e) {
            throw new RuntimeException("SHA-256 not available", e);
        }
    }

    private static byte[] hmacSha256(String data, byte[] key) {
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(key, "HmacSHA256"));
            return mac.doFinal(data.getBytes(StandardCharsets.UTF_8));
        } catch (Exception e) {
            throw new RuntimeException("HMAC-SHA256 failed", e);
        }
    }

    private static String hmacSha256Hex(String data, byte[] key) {
        byte[] raw = hmacSha256(data, key);
        StringBuilder sb = new StringBuilder();
        for (byte b : raw) {
            sb.append(String.format("%02x", b));
        }
        return sb.toString();
    }

    // --- canonicalization helpers ---

    private static String buildCredentialScope(OffsetDateTime signingTime) {
        OffsetDateTime future = signingTime.plusDays(7);
        return future.format(FMT_DATE) + "/" + CREDENTIAL_SCOPE_SUFFIX;
    }

    private static String canonicalURIPath(String path) {
        path = path.trim();
        if (path.length() > 1 && path.endsWith("/")) {
            path = path.substring(0, path.length() - 1);
        }
        return path;
    }

    private static String canonicalQuery(String method, String rawQuery) {
        if (METHODS_WITH_BODY.contains(method.toUpperCase())) {
            return "";
        }
        if (rawQuery.startsWith("?")) {
            rawQuery = rawQuery.substring(1);
        }
        return rawQuery.replace("+", "%20");
    }
}
