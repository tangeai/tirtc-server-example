import { useSyncExternalStore } from 'react';
import { Alert, Typography } from 'antd';
import {
  errorFeedback,
  type AdminProblem,
  type ErrorFeedbackItem,
  type ErrorFeedbackStore,
} from './error-feedback';

export function AdminProblemAlert({
  problem,
  onClose,
}: {
  problem: AdminProblem;
  onClose?: () => void;
}) {
  const reference = [
    problem.code !== undefined ? `业务码：${problem.code}` : '',
    problem.status !== undefined ? `HTTP ${problem.status}` : '',
  ]
    .filter(Boolean)
    .join(' · ');
  return (
    <Alert
      role="alert"
      type="error"
      showIcon
      closable={Boolean(onClose)}
      onClose={onClose}
      message={problem.message}
      description={
        <div>
          {reference && (
            <Typography.Text className="admin-error-reference" type="secondary">
              {reference}
            </Typography.Text>
          )}
          {problem.suggestions.length > 0 && (
            <div className="admin-error-suggestions">
              <Typography.Text strong>建议处理：</Typography.Text>
              <ul>
                {problem.suggestions.map((suggestion) => (
                  <li key={suggestion}>{suggestion}</li>
                ))}
              </ul>
            </div>
          )}
        </div>
      }
    />
  );
}

function FeedbackItems({
  items,
  store,
}: {
  items: ErrorFeedbackItem[];
  store: ErrorFeedbackStore;
}) {
  return items.map((item) => (
    <AdminProblemAlert
      key={item.id}
      problem={item.problem}
      onClose={() => store.dismiss(item.id)}
    />
  ));
}

export function ErrorFeedbackHost({ store = errorFeedback }: { store?: ErrorFeedbackStore }) {
  const items = useSyncExternalStore(store.subscribe, store.getSnapshot, store.getSnapshot);
  if (!items.length) return null;
  return (
    <div className="admin-error-feedback" aria-live="assertive" aria-relevant="additions">
      <FeedbackItems items={items} store={store} />
    </div>
  );
}
