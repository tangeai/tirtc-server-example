export type ServiceStatusRow = Record<string, unknown> & {
  service?: string;
  status?: string;
  instances?: Array<Record<string, unknown>> | null;
};

export type ConfigBadge = {
  color: 'gold' | 'error' | 'processing' | 'success';
  label: string;
};

type ConfigBadgeDefinition = {
  required: boolean;
  blocking: boolean;
  test_kind?: string;
  reload: 'runtime' | 'restart';
};

export function configBadges(
  definition: ConfigBadgeDefinition,
  configured: boolean,
): ConfigBadge[] {
  const badges: ConfigBadge[] = [];
  if (configured && (definition.required || definition.blocking || definition.test_kind)) {
    badges.push({
      color: 'success',
      label: definition.test_kind ? '已测试可用' : '已配置',
    });
  }
  if (!configured && definition.required) badges.push({ color: 'gold', label: '必填' });
  if (!configured && definition.blocking) {
    badges.push({ color: 'error', label: '未配置，将阻塞业务' });
  }
  if (definition.reload === 'restart') {
    badges.push({
      color: 'processing',
      label: configured ? '配置变更后需服务器重启' : '配置后需服务器重启',
    });
  }
  return badges;
}

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
