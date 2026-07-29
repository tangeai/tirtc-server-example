#ifndef MEDIA_SUBSCRIPTION_POLICY_H
#define MEDIA_SUBSCRIPTION_POLICY_H

typedef struct {
    int initialized;
    int video_capable;
    int video_enabled;
} MediaSubscriptionPolicy;

/*
 * Prepare one call session. Video starts enabled only when the negotiated call
 * type and the configured media source both support it.
 */
void media_subscription_policy_prepare(MediaSubscriptionPolicy *policy,
                                       int video_capable);
void media_subscription_policy_reset(MediaSubscriptionPolicy *policy);

/* Return non-zero when a peer video subscription can be accepted. */
int media_subscription_policy_subscribe_video(MediaSubscriptionPolicy *policy);
void media_subscription_policy_unsubscribe_video(MediaSubscriptionPolicy *policy);
int media_subscription_policy_video_enabled(
    const MediaSubscriptionPolicy *policy);

#endif
