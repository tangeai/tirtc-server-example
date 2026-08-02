/** \file device_reference.h
 * \brief Reusable entry point for the Linux C reference runtime.
 */
#ifndef DEVICE_REFERENCE_H
#define DEVICE_REFERENCE_H

#ifdef __cplusplus
extern "C" {
#endif

/** Run the complete reference orchestration using command-line style options.
 * A product may install DeviceAdapterV1 first; otherwise the stock Linux
 * adapter is installed. This call owns the runtime until exit is requested and
 * returns a process-style status code. It is not safe to call concurrently. */
int device_reference_run(int argc, char *argv[]);

#ifdef __cplusplus
}
#endif

#endif /* DEVICE_REFERENCE_H */
