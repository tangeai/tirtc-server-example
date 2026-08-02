#ifndef LINUX_DEVICE_ADAPTER_H
#define LINUX_DEVICE_ADAPTER_H

#include "device_adapter.h"

/** Populate a V1 table with the stock POSIX, JSON identity, file-media and
 * stdin implementation. Products may replace selected groups before install.
 */
int linux_device_adapter_build(DeviceAdapterV1 *adapter_out);

/** Install the default Linux/POSIX, JSON identity, file-media and stdin
 * adapter. Existing custom adapters are left untouched. */
int linux_device_adapter_install_default(void);

#endif
