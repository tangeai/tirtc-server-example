<?php
/**
 * TGV1-HMAC-SHA256 request signing for tange.ai server APIs.
 *
 * Usage:
 *   require_once 'TirtcSigning.php';
 *   $headers = TirtcSigning::signRequest($accessKey, $accessSecret, $appId, 'POST', '/v2/user/login/user-id', '{"user_id":"test"}');
 */

class TirtcSigning {
    const ALG_TGV1 = 'TGV1-HMAC-SHA256';

    /**
     * @param string $accessKey
     * @param string $accessSecret
     * @param string $appId
     * @param string $method
     * @param string $uriPath
     * @param string $rawQuery
     * @param string|null $body
     * @param DateTime|null $signingTime
     * @return array<string, string>
     */
    public static function signRequest(
        string $accessKey,
        string $accessSecret,
        string $appId,
        string $method,
        string $uriPath,
        string $rawQuery = '',
        ?string $body = null,
        ?DateTime $signingTime = null
    ): array {
        $method = strtoupper($method);
        if ($signingTime === null) {
            $signingTime = new DateTime('now', new DateTimeZone('UTC'));
        }
        $now = $signingTime;
        $bodyStr = $body ?? '';
        $payloadHash = self::sha256Hex($bodyStr);

        // Step 1: build header map
        $hv = [
            'X-Tg-Algorithm' => self::ALG_TGV1,
            'X-Tg-Date' => $now->format('Ymd\THis\Z'),
            'X-Tg-App-Id' => trim($appId),
            'X-Tg-Content-Sha256' => $payloadHash,
        ];
        // Signed headers: only x-tg-app-id, x-tg-date (+ content-type, content-length for body methods)
        $signedNames = ['X-Tg-App-Id', 'X-Tg-Date'];
        if (in_array($method, ['POST', 'PUT', 'PATCH'])) {
            $hv['Content-Type'] = 'application/json';
            $hv['Content-Length'] = (string)strlen($bodyStr);
            array_unshift($signedNames, 'Content-Length', 'Content-Type');
        }

        // Step 2: sorted signed header names
        $signedHdrs = implode(';', array_map('strtolower', $signedNames));

        // Step 3: canonical headers string
        $lines = array_map(function($k) use ($hv) {
            return strtolower($k) . ':' . $hv[$k];
        }, $signedNames);
        $canonHdrs = implode("\n", $lines);

        // Step 4: canonical request
        $uri = self::canonicalURI($uriPath);
        $qCanon = self::canonicalQuery($method, ltrim($rawQuery, '?'));
        $canonReq = implode("\n", [$method, $uri, $qCanon, $canonHdrs, $signedHdrs, $payloadHash]);

        // Step 5: string to sign
        $scopeDate = (clone $now)->modify('+7 days')->format('Ymd');
        $scope = "$scopeDate/tgv1_request";
        $strToSign = implode("\n", [self::ALG_TGV1, $hv['X-Tg-Date'], $scope, self::sha256Hex($canonReq)]);

        // Step 6: derive signing key
        $k = self::hmacSha256($now->format('Ymd'), 'TGV1' . $accessSecret, true);
        $k = self::hmacSha256($uri, $k, true);
        $k = self::hmacSha256('tgv1_request', $k, true);

        // Step 7: signature
        $sig = self::hmacSha256($strToSign, $k);

        // Step 8: Authorization
        $auth = self::ALG_TGV1 . " Credential=$accessKey/$scope, SignedHeaders=$signedHdrs, Signature=$sig";

        $out = [
            'X-Tg-Algorithm' => $hv['X-Tg-Algorithm'],
            'X-Tg-Date' => $hv['X-Tg-Date'],
            'X-Tg-App-Id' => $hv['X-Tg-App-Id'],
            'X-Tg-Content-Sha256' => $hv['X-Tg-Content-Sha256'],
            'X-Tg-Signed-Headers' => $signedHdrs,
            'Authorization' => $auth,
        ];
        if (isset($hv['Content-Type'])) $out['Content-Type'] = $hv['Content-Type'];
        if (isset($hv['Content-Length'])) $out['Content-Length'] = $hv['Content-Length'];
        return $out;
    }

    private static function sha256Hex(string $data): string {
        return hash('sha256', $data);
    }

    private static function hmacSha256(string $data, $key, bool $raw = false): string {
        return hash_hmac('sha256', $data, $key, $raw);
    }

    private static function canonicalURI(string $p): string {
        $p = trim($p);
        if (strlen($p) > 1 && str_ends_with($p, '/')) {
            $p = substr($p, 0, -1);
        }
        return $p;
    }

    private static function canonicalQuery(string $method, string $rawQuery): string {
        if (in_array(strtoupper($method), ['POST', 'PUT', 'PATCH'])) {
            return '';
        }
        return str_replace('+', '%20', $rawQuery);
    }
}
