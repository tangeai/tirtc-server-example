# Makefile — Linux C device reference implementation
# Target: Linux user space. Other operating systems require a separate port.

TARGET  := device-sim
PRODUCT_TARGET := device-sim-product
FRAMEWORK_TARGET := libdevice-reference.a
SRCDIR  := src
OBJDIR  := obj
AR      ?= ar
WERROR  ?= 0
SANITIZE ?=
SYSROOT ?=
PKG_CONFIG ?= pkg-config
EXTRA_CFLAGS ?=
EXTRA_LDFLAGS ?=
PRODUCT_ADAPTER_OBJ ?=

# The repository keeps SDK releases under <platform>/<version>.  Allow the
# target platform/version to be selected for native or cross compilation.
SDK_PLATFORM ?= linux-x86_64
SDK_VERSION  ?= 2.2.1
SDK_DIR      ?= ../sdk/$(SDK_PLATFORM)/$(SDK_VERSION)
MBEDTLS_SDK_VERSION ?= 0.1.6
MBEDTLS_SDK_DIR ?= ../sdk/$(SDK_PLATFORM)/$(MBEDTLS_SDK_VERSION)
SDK_INC := -I$(SDK_DIR)/include
SDK_LIB := -L$(SDK_DIR)/lib -lTiRTC

CURL_CFLAGS ?= $(shell $(PKG_CONFIG) --cflags libcurl 2>/dev/null)
CURL_LIBS   ?= $(shell $(PKG_CONFIG) --libs libcurl 2>/dev/null)
CJSON_CFLAGS ?=
CJSON_LIBS   ?= -lcjson
MOSQUITTO_CFLAGS ?=
MOSQUITTO_LIBS   ?= -lmosquitto
RPATH ?= $$ORIGIN/../sdk/$(SDK_PLATFORM)/$(SDK_VERSION)/lib

CFLAGS  := -Wall -Wextra -std=gnu99 -g -O2 -pthread -D_GNU_SOURCE
CFLAGS  += $(SDK_INC) -I$(MBEDTLS_SDK_DIR)/include
CFLAGS  += $(CURL_CFLAGS) $(CJSON_CFLAGS) $(MOSQUITTO_CFLAGS)
ifneq ($(strip $(SYSROOT)),)
CFLAGS  += --sysroot=$(SYSROOT)
endif
ifeq ($(WERROR),1)
CFLAGS  += -Werror
endif
ifneq ($(SANITIZE),)
CFLAGS  += -fsanitize=$(SANITIZE) -fno-omit-frame-pointer
endif
CFLAGS  += $(EXTRA_CFLAGS)

LDFLAGS := -pthread
ifneq ($(strip $(RPATH)),)
LDFLAGS += -Wl,-rpath,'$(RPATH)'
endif
LDFLAGS += $(SDK_LIB)
LDFLAGS += $(CURL_LIBS) $(CJSON_LIBS) $(MOSQUITTO_LIBS)
LDFLAGS += -lm
ifneq ($(strip $(SYSROOT)),)
LDFLAGS += --sysroot=$(SYSROOT)
endif
ifneq ($(SANITIZE),)
LDFLAGS += -fsanitize=$(SANITIZE)
endif
LDFLAGS += $(EXTRA_LDFLAGS)

SRCS := $(wildcard $(SRCDIR)/*.c)
OBJS := $(SRCS:$(SRCDIR)/%.c=$(OBJDIR)/%.o)
FRAMEWORK_OBJS := $(filter-out $(OBJDIR)/reference_main.o,$(OBJS))
DEPS := $(OBJS:.o=.d)

.PHONY: all framework product adapter-template-check clean run help check-sdk test

all: check-sdk $(TARGET)

framework: check-sdk $(FRAMEWORK_TARGET)

product: check-sdk $(OBJS)
	@test -n "$(strip $(PRODUCT_ADAPTER_OBJ))" || \
		( echo "Set PRODUCT_ADAPTER_OBJ to a compiled product adapter object."; \
		  exit 1 )
	@echo "  LD    $(PRODUCT_TARGET)"
	@$(CC) $(CFLAGS) -o $(PRODUCT_TARGET) $(OBJS) \
		$(PRODUCT_ADAPTER_OBJ) $(LDFLAGS)

ADAPTER_TEMPLATE_OBJ := $(OBJDIR)/product_adapter_template.o

adapter-template-check: $(ADAPTER_TEMPLATE_OBJ)

$(ADAPTER_TEMPLATE_OBJ): examples/product_adapter_template.c | $(OBJDIR)
	@echo "  CC    $<"
	@$(CC) $(CFLAGS) -I$(SRCDIR) -c $< -o $@

check-sdk:
	@test -f "$(SDK_DIR)/include/tirtc/tiRTC.h" && test -f "$(SDK_DIR)/lib/libTiRTC.so" || \
		( echo "Missing TiRTC C SDK: $(SDK_DIR)"; \
		  echo "Set SDK_PLATFORM and SDK_VERSION to a bundled or downloaded SDK release."; \
		  exit 1 )
	@test -f "$(MBEDTLS_SDK_DIR)/include/mbedtls/md.h" && test -f "$(MBEDTLS_SDK_DIR)/include/mbedtls/base64.h" || \
		( echo "Missing SDK-provided mbedTLS headers: $(MBEDTLS_SDK_DIR)/include/mbedtls"; \
		  echo "Set MBEDTLS_SDK_VERSION to a compatible SDK release."; \
		  exit 1 )

$(TARGET): $(OBJS)
	@echo "  LD    $@"
	@$(CC) $(CFLAGS) -o $@ $^ $(LDFLAGS)

$(FRAMEWORK_TARGET): $(FRAMEWORK_OBJS)
	@echo "  AR    $@"
	@$(AR) rcs $@ $^

$(OBJDIR)/%.o: $(SRCDIR)/%.c | $(OBJDIR)
	@echo "  CC    $<"
	@$(CC) $(CFLAGS) -MMD -MP -c $< -o $@

$(OBJDIR):
	@mkdir -p $(OBJDIR)

-include $(DEPS)

clean:
	rm -rf $(OBJDIR) $(TARGET) $(PRODUCT_TARGET) $(FRAMEWORK_TARGET)

TEST_TARGET := $(OBJDIR)/test-core
TEST_SRCS := tests/test_core.c $(SRCDIR)/common.c $(SRCDIR)/audio_recorder.c \
	$(SRCDIR)/device_adapter.c $(SRCDIR)/linux_device_adapter.c \
	$(SRCDIR)/media_format.c \
	$(SRCDIR)/file_media_source.c $(SRCDIR)/sdk_callback_guard.c \
	$(SRCDIR)/media_subscription_policy.c \
	$(SRCDIR)/session_coordinator.c $(SRCDIR)/session_arbiter.c \
	$(SRCDIR)/device_flow.c $(SRCDIR)/http_tls.c $(SRCDIR)/media_rx_log.c \
	$(SRCDIR)/tirtc_runtime.c $(SRCDIR)/tirtc_ai.c \
	$(SRCDIR)/tirtc_voip.c $(SRCDIR)/tirtc_call.c \
	$(SRCDIR)/call_session.c

$(TEST_TARGET): $(TEST_SRCS) | $(OBJDIR)
	@echo "  CC    $@"
	@$(CC) $(CFLAGS) -DDEVICE_SIM_TESTING -I$(SRCDIR) -o $@ $(TEST_SRCS) $(LDFLAGS)

test: check-sdk $(TEST_TARGET) $(TARGET)
	@LD_LIBRARY_PATH="$(SDK_DIR)/lib:$${LD_LIBRARY_PATH}" $(TEST_TARGET)
	@LD_LIBRARY_PATH="$(SDK_DIR)/lib:$${LD_LIBRARY_PATH}" ./$(TARGET) --help >/dev/null

run: $(TARGET)
	./$(TARGET)

help:
	@echo "Usage:"
	@echo "  make          Build $(TARGET)"
	@echo "  make framework Build reusable $(FRAMEWORK_TARGET) (without main)"
	@echo "  make product PRODUCT_ADAPTER_OBJ=... Build $(PRODUCT_TARGET)"
	@echo "  make adapter-template-check Validate the product adapter template"
	@echo "  make WERROR=1 Build with warnings treated as errors"
	@echo "  make SANITIZE=address WERROR=1  Build with AddressSanitizer"
	@echo "  make CC=<cross-gcc> SYSROOT=<path> SDK_PLATFORM=<target>  Cross-build"
	@echo "  make test     Run C core tests"
	@echo "  make clean    Remove build artifacts"
	@echo ""
	@echo "Run examples:"
	@echo "  # Unified device runtime (streaming, VoIP, AI and device calls)"
	@echo "  DEVICE_ID=DEV000001 DEVICE_KEY=your-key ./$(TARGET)"
	@echo ""
	@echo "  # Unbound flow (scan-code binding)"
	@echo "  ./$(TARGET) --mac AA:BB:CC:DD:EE:FF"
