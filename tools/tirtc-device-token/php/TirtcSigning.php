<?php

/**
 * TGV1-HMAC-SHA256 request signing for tange.ai TiRTC server APIs.
 *
 * Reference: https://tange-ai.feishu.cn/wiki/wikcn6GaETEIVO0jgCP1ASHf3Mg
 *
 * Usage:
 *   require_once 'TirtcSigning.php';
 *   $headers = TirtcSigning::signRequest(
 *       'your-access-key',
 *       'your-access-secret',
 *       'your-app-id',
 *       'POST',
 *       '/v2/device/info',
 *       '{"device_id":"TEST"}'
 *   );
 */

final class TirtcSigning
{
    const TGV1_ALG = 'TGV1-HMAC-SHA256';
    const CREDENTIAL_SCOPE_SUFFIX = 'tgv1_request';

    /**
     * Build TGV1-HMAC-SHA256 signed HTTP headers.
     *
     * @param string $accessKey    Appears in the Authorization Credential field.
     * @param string $accessSecret Used for HMAC key derivation (keep this secret).
     * @param string $appId        TiRTC application ID (X-Tg-App-Id header).
     * @param string $method       HTTP method (GET, POST, PUT, PATCH, DELETE).
     * @param string $uriPath      URI path, e.g. "/v2/device/info".
     * @param string $body         Request body string (empty for GET/DELETE).
     * @param string $rawQuery     Raw query without leading "?" (ignored for POST/PUT/PATCH).
     * @param DateTime|null $signingTime UTC DateTime; uses now if null.
     * @return array<string,string> HTTP header name => value.
     */
    public static function signRequest(
        string $accessKey,
        string $accessSecret,
        string $appId,
        string $method,
        string $uriPath,
        string $body = '',
        string $rawQuery = '',
        ?DateTime $signingTime = null
    ): array {
        $method = strtoupper($method);
        if ($signingTime === null) {
            $signingTime = new DateTime('now', new DateTimeZone('UTC'));
        } else {
            $signingTime->setTimezone(new DateTimeZone('UTC'));
        }

        $tgDate = $signingTime->format('Ymd\THis\Z');
        $scope = self::buildCredentialScope($signingTime);
        $payloadHash = hash('sha256', $body);

        // Step 1: build header values map
        $hv = array(
            'x-tg-algorithm'      => self::TGV1_ALG,
            'x-tg-date'           => $tgDate,
            'x-tg-app-id'         => trim($appId),
            'x-tg-content-sha256' => $payloadHash,
        );
        if (in_array($method, array('POST', 'PUT', 'PATCH'), true)) {
            $hv['content-type'] = 'application/json';
            $hv['content-length'] = (string)strlen($body);
        }

        // Step 2: sorted lowercased header names
        $names = array_keys($hv);
        $names = array_map('strtolower', $names);
        sort($names, SORT_STRING);
        $signedHeaders = implode(';', $names);

        // Step 3: canonical header string
        $canonLines = array();
        foreach ($names as $name) {
            $canonLines[] = $name . ':' . trim($hv[$name]);
        }
        $hCanon = implode("\n", $canonLines);

        // Step 4: canonical request
        $uriP = self::canonicalURIPath($uriPath);
        $qCanon = self::canonicalQuery($method, $rawQuery);
        $canonicalReq = implode("\n", array($method, $uriP, $qCanon, $hCanon, $signedHeaders, $payloadHash));

        // Step 5: string-to-sign
        $hashCanon = hash('sha256', $canonicalReq);
        $strToSign = implode("\n", array(self::TGV1_ALG, $tgDate, $scope, $hashCanon));

        // Step 6: derive signing key
        $k = hash_hmac('sha256', $signingTime->format('Ymd'), 'TGV1' . $accessSecret, true);
        $k = hash_hmac('sha256', $uriP, $k, true);
        $k = hash_hmac('sha256', self::CREDENTIAL_SCOPE_SUFFIX, $k, true);

        // Step 7: compute signature
        $sig = hash_hmac('sha256', $strToSign, $k);

        // Step 8: build Authorization header
        $cred = $accessKey . '/' . $scope;
        $auth = self::TGV1_ALG . ' Credential=' . $cred . ', SignedHeaders=' . $signedHeaders . ', Signature=' . $sig;

        $result = array(
            'X-Tg-Algorithm'       => $hv['x-tg-algorithm'],
            'X-Tg-Date'            => $hv['x-tg-date'],
            'X-Tg-App-Id'          => $hv['x-tg-app-id'],
            'X-Tg-Content-Sha256'  => $hv['x-tg-content-sha256'],
            'X-Tg-Signed-Headers'  => $signedHeaders,
            'Authorization'        => $auth,
        );
        if (isset($hv['content-type'])) {
            $result['Content-Type'] = $hv['content-type'];
        }
        if (isset($hv['content-length'])) {
            $result['Content-Length'] = $hv['content-length'];
        }
        return $result;
    }

    private static function buildCredentialScope(DateTime $signingTime): string
    {
        $future = clone $signingTime;
        $future->modify('+7 days');
        return $future->format('Ymd') . '/' . self::CREDENTIAL_SCOPE_SUFFIX;
    }

    private static function canonicalURIPath(string $path): string
    {
        $path = trim($path);
        if (strlen($path) > 1 && $path[-1] === '/') {
            $path = rtrim($path, '/');
        }
        return $path;
    }

    private static function canonicalQuery(string $method, string $rawQuery): string
    {
        if (in_array(strtoupper($method), array('POST', 'PUT', 'PATCH'), true)) {
            return '';
        }
        if (str_starts_with($rawQuery, '?')) {
            $rawQuery = substr($rawQuery, 1);
        }
        return str_replace('+', '%20', $rawQuery);
    }
}
