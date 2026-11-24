-- Evaluated inside the room store's atomic operation. The adapter supplies
-- the storage primitives. All participant, ownership and notification rules
-- for a transition are defined here.
--
-- Primitives the adapter must define before this chunk (r is a decoded room
-- table with room_id, caller, targets, status, answered_by, call_type):
--   room(id) -> r | nil              owned_room(device) -> id | false
--   room_winner(id) -> device | false  room_ttl(id) -> remaining ms
--   insert_room(r, offline, ttl)     save_answer(id, device, ttl)
--   release(r)                       unlock(device, id)
--   add_rejection(id, device) -> bool  rejected_by(id, device) -> bool
--   enqueue(r, target, kind, from, payload)
local function contains(values, candidate)
    for _, value in ipairs(values) do
        if value == candidate then return true end
    end
    return false
end

local function notify_targets(targets, excluded, send)
    local sent = {}
    for _, target in ipairs(targets) do
        if target ~= excluded and not sent[target] then
            sent[target] = true
            send(target)
        end
    end
end

local function notify_end(r, actor, reason, all_targets)
    local targets = r.targets
    if not all_targets then
        if r.caller == actor and r.answered_by and r.answered_by ~= '' then
            targets = {r.answered_by}
        elseif r.answered_by == actor then
            targets = {r.caller}
        end
    end
    notify_targets(targets, actor, function(target)
        enqueue(r, target, 'room_cancel', '', {room_id=r.room_id, reason=reason})
    end)
end

local function create_room(r, offline, ttl)
    local existing = owned_room(r.caller)
    if existing then return existing end
    insert_room(r, offline, ttl)
    notify_targets(r.targets, nil, function(target)
        if contains(offline, target) then return end
        local name = r.caller_names and r.caller_names[target]
        if not name or name == '' then name = r.caller end
        enqueue(r, target, 'call_incoming', r.caller, {
            room_id=r.room_id, caller_id=r.caller, caller_name=name, call_type=r.call_type
        })
    end)
    return ''
end

local function answer_room(id, caller, actor)
    local r = room(id)
    if not r then return 'not_found' end
    if r.caller ~= caller or not contains(r.targets, actor) then return 'forbidden' end
    local winner = room_winner(id) or r.answered_by
    if winner and winner ~= '' and winner ~= actor then return 'answered' end
    if r.status == 'answered' and r.answered_by == actor then
        return cjson.encode({room=r, first=false})
    end
    local ttl = room_ttl(id)
    if ttl <= 0 then return 'not_found' end
    local previous_id = owned_room(actor)
    local previous
    if previous_id and previous_id ~= id then
        previous = room(previous_id)
        if previous and (previous.caller == actor or previous.answered_by == actor) then
            release(previous)
            notify_end(previous, actor, 'caller_left', false)
        else
            previous = nil
            unlock(actor, previous_id)
        end
    end
    save_answer(id, actor, ttl)
    r.status = 'answered'
    r.answered_by = actor
    enqueue(r, caller, 'callee_answered', '', {room_id=id, callee_id=actor})
    notify_targets(r.targets, actor, function(target)
        enqueue(r, target, 'room_cancel', '', {room_id=id, reason='accepted_by_other'})
    end)
    return cjson.encode({room=r, previous=previous, first=true})
end

local function close_room(r, actor, caller_only, reason)
    release(r)
    notify_end(r, actor, reason, caller_only or (reason == 'unbound' and r.caller == actor))
    return cjson.encode(r)
end

local function end_room(id, actor, caller_only, reason)
    local r = room(id)
    if not r then return 'not_found' end
    if r.caller ~= actor and (caller_only or r.answered_by ~= actor) then return 'forbidden' end
    return close_room(r, actor, caller_only, reason)
end

-- Resolves the device's room at commit time, so a concurrent auto-switch
-- cannot leave the device in the room it moved to.
local function end_owned_room(actor, reason)
    local id = owned_room(actor)
    if not id then return '' end
    local r = room(id)
    if not r or (r.caller ~= actor and r.answered_by ~= actor) then
        unlock(actor, id)
        return ''
    end
    return close_room(r, actor, false, reason)
end

local function reject_room(id, actor, reason)
    local r = room(id)
    if not r then return 'not_found' end
    if not contains(r.targets, actor) then return 'forbidden' end
    if r.status == 'answered' or room_winner(id) then
        return cjson.encode({room=r, applied=false, all_rejected=false})
    end
    local applied = add_rejection(id, actor)
    if applied then enqueue(r, r.caller, 'call_reject', actor, {room_id=id, reason=reason}) end
    local all = true
    for _, target in ipairs(r.targets) do
        if not rejected_by(id, target) then all = false; break end
    end
    if all then
        release(r)
        enqueue(r, r.caller, 'room_cancel', '', {room_id=id, reason='all_rejected'})
    end
    return cjson.encode({room=r, applied=applied, all_rejected=all})
end
