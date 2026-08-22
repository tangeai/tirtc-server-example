export type ServiceStatusRow = Record<string, unknown> & {
  service?: string;
  status?: string;
  instances?: Array<Record<string, unknown>> | null;
};

// Kept outside the React view so offline/null status payloads have a focused
// regression seam. The view and this test exercise the same normalization.
export function commonServiceRows(services: ServiceStatusRow[] | undefined) {
  return (services ?? []).flatMap((service) => {
    const instances = Array.isArray(service.instances) ? service.instances : [];
    return instances.length
      ? instances.map((instance) => ({
          ...instance,
          service: service.service,
          status: service.status,
        }))
      : [
          {
            service: service.service,
            status: service.status,
            instance_id: '—',
            node: '—',
          },
        ];
  });
}
