-- A left assignment has no current room relationship. Clear historical owners
-- left behind by unbind cleanup before the invariant was enforced.
UPDATE call_assignments
SET owner_user_id = 0
WHERE desired_state = 'left'
  AND owner_user_id <> 0;
