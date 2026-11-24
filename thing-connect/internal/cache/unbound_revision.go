package cache

// The marker shares the atomic operation with Redis cleanup. A duplicate
// already past its SQL ownership check cannot disturb a newer session. Keep
// one high-water mark per device across room expiry and service restarts.
const unbindRevisionCheck = `
local function already_cleaned(key,revision)
 local old=redis.call('GET',key)
 return old and (#old>#revision or (#old==#revision and old>=revision))
end
`
