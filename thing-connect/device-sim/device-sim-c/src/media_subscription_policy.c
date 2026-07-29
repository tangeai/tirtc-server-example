#include "media_subscription_policy.h"

#include <string.h>

void media_subscription_policy_prepare(MediaSubscriptionPolicy *policy,
                                       int video_capable) {
    if (!policy) return;
    policy->initialized = 1;
    policy->video_capable = video_capable ? 1 : 0;
    policy->video_enabled = policy->video_capable;
}

void media_subscription_policy_reset(MediaSubscriptionPolicy *policy) {
    if (!policy) return;
    memset(policy, 0, sizeof(*policy));
}

int media_subscription_policy_subscribe_video(MediaSubscriptionPolicy *policy) {
    if (!policy || !policy->initialized || !policy->video_capable)
        return 0;
    policy->video_enabled = 1;
    return 1;
}

void media_subscription_policy_unsubscribe_video(MediaSubscriptionPolicy *policy) {
    if (policy) policy->video_enabled = 0;
}

int media_subscription_policy_video_enabled(
    const MediaSubscriptionPolicy *policy) {
    return policy && policy->initialized && policy->video_capable &&
           policy->video_enabled;
}
