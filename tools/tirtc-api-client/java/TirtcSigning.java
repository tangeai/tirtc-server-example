import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.ZoneOffset;
import java.time.ZonedDateTime;
import java.time.format.DateTimeFormatter;
import java.util.*;

/**
 * TGV1-HMAC-SHA256 request signing for tange.ai server APIs.
 *
 * Usage:
 *   Map<String, String> headers = TirtcSigning.signRequest(accessKey, accessSecret, appId,
 *       "POST", "/v2/user/login/user-id", "", "{\"user_id\":\"test\"}", null);
 */
public class TirtcSigning {
    private static final String ALG_TGV1 = "TGV1-HMAC-SHA256";

    public static Map<String, String> signRequest(
            String accessKey, String accessSecret, String appId,
            String method, String uriPath, String rawQuery,
            String body, ZonedDateTime signingTime) {

        method = method.toUpperCase();
        if (signingTime == null) {
            signingTime = ZonedDateTime.now(ZoneOffset.UTC);
        }
        ZonedDateTime now = signingTime;
        String bodyStr = body != null ? body : "";
        String payloadHash = sha256Hex(bodyStr);

        DateTimeFormatter tgFmt = DateTimeFormatter.ofPattern("yyyyMMdd'T'HHmmss'Z'");

        // Step 1: build header map
        Map<String, String> hv = new LinkedHashMap<>();
        hv.put("X-Tg-Algorithm", ALG_TGV1);
        hv.put("X-Tg-Date", now.format(tgFmt));
        hv.put("X-Tg-App-Id", appId.trim());
        hv.put("X-Tg-Content-Sha256", payloadHash);

        // Signed headers: only x-tg-app-id, x-tg-date (+ content-type, content-length for body methods)
        List<String> signedNames = new ArrayList<>(Arrays.asList("X-Tg-App-Id", "X-Tg-Date"));
        if (Arrays.asList("POST", "PUT", "PATCH").contains(method)) {
            hv.put("Content-Type", "application/json");
            hv.put("Content-Length", String.valueOf(bodyStr.getBytes(StandardCharsets.UTF_8).length));
            signedNames.add(0, "Content-Length");
            signedNames.add(0, "Content-Type");
        }

        // Step 2: signed header names string
        List<String> lowerNames = new ArrayList<>();
        for (String n : signedNames) lowerNames.add(n.toLowerCase());
        String signedHdrs = String.join(";", lowerNames);

        // Step 3: canonical headers string
        List<String> lines = new ArrayList<>();
        for (String k : signedNames) {
            lines.add(k.toLowerCase() + ":" + hv.get(k).trim());
        }
        String canonHdrs = String.join("\n", lines);

        // Step 4: canonical request
        String uri = canonicalURI(uriPath);
        String qCanon = canonicalQuery(method, rawQuery != null ? rawQuery.replaceFirst("^\\?", "") : "");
        String canonReq = String.join("\n", method, uri, qCanon, canonHdrs, signedHdrs, payloadHash);

        // Step 5: string to sign
        String scopeDate = now.plusDays(7).format(DateTimeFormatter.ofPattern("yyyyMMdd"));
        String scope = scopeDate + "/tgv1_request";
        String strToSign = String.join("\n", ALG_TGV1, hv.get("X-Tg-Date"), scope, sha256Hex(canonReq));

        // Step 6: derive signing key
        byte[] k = hmacSha256(now.format(DateTimeFormatter.ofPattern("yyyyMMdd")), ("TGV1" + accessSecret).getBytes(StandardCharsets.UTF_8));
        k = hmacSha256(uri, k);
        k = hmacSha256("tgv1_request", k);

        // Step 7: signature
        String sig = hmacSha256Hex(strToSign, k);

        // Step 8: Authorization
        String auth = ALG_TGV1 + " Credential=" + accessKey + "/" + scope + ", SignedHeaders=" + signedHdrs + ", Signature=" + sig;

        Map<String, String> out = new LinkedHashMap<>();
        out.put("X-Tg-Algorithm", hv.get("X-Tg-Algorithm"));
        out.put("X-Tg-Date", hv.get("X-Tg-Date"));
        out.put("X-Tg-App-Id", hv.get("X-Tg-App-Id"));
        out.put("X-Tg-Content-Sha256", hv.get("X-Tg-Content-Sha256"));
        out.put("X-Tg-Signed-Headers", signedHdrs);
        out.put("Authorization", auth);
        if (hv.containsKey("Content-Type")) out.put("Content-Type", hv.get("Content-Type"));
        if (hv.containsKey("Content-Length")) out.put("Content-Length", hv.get("Content-Length"));
        return out;
    }

    private static String sha256Hex(String data) {
        try {
            MessageDigest md = MessageDigest.getInstance("SHA-256");
            byte[] hash = md.digest(data.getBytes(StandardCharsets.UTF_8));
            StringBuilder sb = new StringBuilder();
            for (byte b : hash) sb.append(String.format("%02x", b));
            return sb.toString();
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
    }

    private static byte[] hmacSha256(String data, byte[] key) {
        try {
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(key, "HmacSHA256"));
            return mac.doFinal(data.getBytes(StandardCharsets.UTF_8));
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
    }

    private static String hmacSha256Hex(String data, byte[] key) {
        byte[] hash = hmacSha256(data, key);
        StringBuilder sb = new StringBuilder();
        for (byte b : hash) sb.append(String.format("%02x", b));
        return sb.toString();
    }

    private static String canonicalURI(String p) {
        p = p.trim();
        if (p.length() > 1 && p.endsWith("/")) p = p.substring(0, p.length() - 1);
        return p;
    }

    private static String canonicalQuery(String method, String rawQuery) {
        if (List.of("POST", "PUT", "PATCH").contains(method.toUpperCase())) return "";
        return rawQuery.replace("+", "%20");
    }
}
