#ifndef MEDIA_SUBSCRIPTION_POLICY_H
#define MEDIA_SUBSCRIPTION_POLICY_H

typedef struct {
    int initialized;
    int video_capable;
    int audio_enabled;
    int video_enabled;
} MediaSubscriptionPolicy;

/*
 * Prepare one call session. Audio and video both start disabled and are enabled
 * independently only by the peer's subscription callbacks.
 */
void media_subscription_policy_prepare(MediaSubscriptionPolicy *policy,
                                       int video_capable);
void media_subscription_policy_reset(MediaSubscriptionPolicy *policy);

/* Outbound media remains disabled until the peer subscribes to each stream. */
int media_subscription_policy_subscribe_audio(MediaSubscriptionPolicy *policy);
void media_subscription_policy_unsubscribe_audio(MediaSubscriptionPolicy *policy);
int media_subscription_policy_audio_enabled(
    const MediaSubscriptionPolicy *policy);
int media_subscription_policy_subscribe_video(MediaSubscriptionPolicy *policy);
void media_subscription_policy_unsubscribe_video(MediaSubscriptionPolicy *policy);
int media_subscription_policy_video_enabled(
    const MediaSubscriptionPolicy *policy);

#endif
