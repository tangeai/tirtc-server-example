function safeDecodeURIComponent(value) {
  try {
    return decodeURIComponent(value)
  } catch (_) {
    return value
  }
}

function appendQueryItem(result, item) {
  const index = item.indexOf('=')
  const key = safeDecodeURIComponent(index >= 0 ? item.slice(0, index) : item)
  const value = safeDecodeURIComponent(index >= 0 ? item.slice(index + 1) : '')
  if (key) result[key] = value
}

function parseQueryString(query) {
  return query.split('&').reduce((result, item) => {
    appendQueryItem(result, item)
    return result
  }, {})
}

function parseIncomingQuery(options, source, trace) {
  const log = typeof trace === 'function' ? trace : () => {}
  log(source + '.options.raw', options)
  if (!options) {
    log(source + '.query.parsed', {})
    return {}
  }
  const query = options.query || options
  log(source + '.query.input', {
    type: typeof query,
    value: query,
  })
  if (typeof query === 'string') {
    const parsed = parseQueryString(query.replace(/\\u0026/gi, '&'))
    log(source + '.query.parsed', parsed)
    return parsed
  }
  if (!query || typeof query !== 'object') {
    log(source + '.query.parsed', {})
    return {}
  }

  const parsed = Object.keys(query).reduce((result, key) => {
    const value = query[key]
    if (typeof value !== 'string') {
      result[key] = value
      return result
    }

    // 微信设备入呼可能把 query 的 \u0026 分隔符原样放进首个字段值，
    // 例如 aspect_ratio: "1.3333\u0026camera_rotation=270"。
    const normalized = value.replace(/\\u0026/gi, '&')
    const parts = normalized.split('&')
    if (parts.length > 1) {
      log(source + '.query.field-expanded', {
        key,
        raw: value,
        normalized,
        parts,
      })
    }
    result[key] = safeDecodeURIComponent(parts.shift())
    parts.forEach(item => appendQueryItem(result, item))
    return result
  }, {})
  log(source + '.query.parsed', parsed)
  return parsed
}

module.exports = {
  parseIncomingQuery,
}
