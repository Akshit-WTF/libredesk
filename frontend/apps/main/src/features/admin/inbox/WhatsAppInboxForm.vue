<template>
  <form @submit="onSubmit" class="space-y-6 w-full max-w-2xl">
    <!-- Inbox name -->
    <FormField v-slot="{ componentField }" name="name">
      <FormItem>
        <FormLabel>{{ $t('globals.terms.name') }}</FormLabel>
        <FormControl>
          <Input type="text" placeholder="e.g. Support WhatsApp" v-bind="componentField" />
        </FormControl>
        <FormMessage />
      </FormItem>
    </FormField>

    <!-- Twilio Account SID -->
    <FormField v-slot="{ componentField }" name="config.account_sid">
      <FormItem>
        <FormLabel>{{ $t('admin.inbox.whatsapp.accountSid') }}</FormLabel>
        <FormControl>
          <Input type="text" placeholder="ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx" v-bind="componentField" />
        </FormControl>
        <FormDescription>
          {{ $t('admin.inbox.whatsapp.accountSid.description') }}
        </FormDescription>
        <FormMessage />
      </FormItem>
    </FormField>

    <!-- Twilio Auth Token -->
    <FormField v-slot="{ componentField }" name="config.auth_token">
      <FormItem>
        <FormLabel>{{ $t('admin.inbox.whatsapp.authToken') }}</FormLabel>
        <FormControl>
          <Input type="password" placeholder="••••••••••••••••••••••••••••••••" v-bind="componentField" />
        </FormControl>
        <FormDescription>
          {{ $t('admin.inbox.whatsapp.authToken.description') }}
        </FormDescription>
        <FormMessage />
      </FormItem>
    </FormField>

    <!-- From number -->
    <FormField v-slot="{ componentField }" name="config.from_number">
      <FormItem>
        <FormLabel>{{ $t('admin.inbox.whatsapp.fromNumber') }}</FormLabel>
        <FormControl>
          <Input type="text" placeholder="+14155238886" v-bind="componentField" />
        </FormControl>
        <FormDescription>
          {{ $t('admin.inbox.whatsapp.fromNumber.description') }}
        </FormDescription>
        <FormMessage />
      </FormItem>
    </FormField>

    <!-- Webhook URL (read-only, shown on edit) -->
    <div v-if="webhookURL" class="space-y-1">
      <p class="text-sm font-medium leading-none">{{ $t('admin.inbox.whatsapp.webhookUrl') }}</p>
      <div class="flex items-center gap-2">
        <Input type="text" :model-value="webhookURL" readonly class="font-mono text-xs" />
        <Button type="button" variant="outline" size="sm" @click="copyWebhookURL">
          {{ copied ? $t('globals.messages.copied') : $t('globals.messages.copy') }}
        </Button>
      </div>
      <p class="text-xs text-muted-foreground">
        {{ $t('admin.inbox.whatsapp.webhookUrl.description') }}
      </p>
    </div>

    <!-- Enabled -->
    <FormField v-slot="{ componentField, handleChange }" name="enabled">
      <FormItem>
        <SwitchField
          :title="$t('globals.terms.enabled')"
          :description="$t('admin.inbox.enabled.description')"
          :checked="componentField.modelValue"
          @update:checked="handleChange"
        />
      </FormItem>
    </FormField>

    <!-- CSAT -->
    <FormField v-slot="{ componentField, handleChange }" name="csat_enabled">
      <FormItem>
        <SwitchField
          :title="$t('admin.inbox.csatSurveys')"
          :description="$t('admin.inbox.csatSurveys.description_1')"
          :checked="componentField.modelValue"
          @update:checked="handleChange"
        />
      </FormItem>
    </FormField>

    <!-- Prompt tags on reply -->
    <FormField v-slot="{ componentField, handleChange }" name="prompt_tags_on_reply">
      <FormItem>
        <SwitchField
          :title="$t('admin.inbox.promptTagsOnReply')"
          :description="$t('admin.inbox.promptTagsOnReply.description')"
          :checked="componentField.modelValue"
          @update:checked="handleChange"
        />
      </FormItem>
    </FormField>

    <!-- Initiation template (first outbound message) -->
    <FormField v-slot="{ componentField }" name="config.init_content_sid">
      <FormItem>
        <FormLabel>{{ $t('admin.inbox.whatsapp.initContentSid') }}</FormLabel>
        <FormControl>
          <Input type="text" placeholder="HXabc123..." v-bind="componentField" />
        </FormControl>
        <FormDescription>
          {{ $t('admin.inbox.whatsapp.initContentSid.description') }}
        </FormDescription>
        <FormMessage />
      </FormItem>
    </FormField>

    <!-- Re-engagement template (24h window) -->
    <FormField v-slot="{ componentField }" name="config.content_sid">
      <FormItem>
        <FormLabel>{{ $t('admin.inbox.whatsapp.contentSid') }}</FormLabel>
        <FormControl>
          <Input type="text" placeholder="HXabc123..." v-bind="componentField" />
        </FormControl>
        <FormDescription>
          {{ $t('admin.inbox.whatsapp.contentSid.description') }}
        </FormDescription>
        <FormMessage />
      </FormItem>
    </FormField>

    <Button type="submit" :disabled="isLoading">
      {{ resolvedSubmitLabel }}
    </Button>
  </form>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { createFormSchema } from './whatsappFormSchema.js'
import {
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
  FormDescription
} from '@shared-ui/components/ui/form/index.js'
import { Input } from '@shared-ui/components/ui/input/index.js'
import SwitchField from '@shared-ui/components/SwitchField.vue'
import { Button } from '@shared-ui/components/ui/button/index.js'
import { useI18n } from 'vue-i18n'

const props = defineProps({
  initialValues: {
    type: Object,
    default: () => ({})
  },
  inboxUUID: {
    type: String,
    default: ''
  },
  submitForm: {
    type: Function,
    required: true
  },
  submitLabel: {
    type: String,
    default: ''
  },
  isNewForm: {
    type: Boolean,
    default: false
  },
  isLoading: {
    type: Boolean,
    default: false
  }
})

const { t } = useI18n()
const copied = ref(false)

// Build the Twilio webhook URL for this inbox (only available when editing).
const webhookURL = computed(() => {
  const uuid = props.inboxUUID || props.initialValues?.uuid
  if (!uuid) return ''
  const base = window.location.origin
  return `${base}/api/v1/inboxes/whatsapp/${uuid}/webhook`
})

const resolvedSubmitLabel = computed(
  () =>
    props.submitLabel ||
    (props.isNewForm ? t('globals.messages.create') : t('globals.messages.save'))
)

const form = useForm({
  validationSchema: toTypedSchema(createFormSchema(t)),
  initialValues: {
    name: props.initialValues?.name ?? '',
    enabled: props.initialValues?.enabled ?? true,
    csat_enabled: props.initialValues?.csat_enabled ?? false,
    prompt_tags_on_reply: props.initialValues?.prompt_tags_on_reply ?? false,
    config: {
      account_sid: props.initialValues?.config?.account_sid ?? '',
      auth_token: props.initialValues?.config?.auth_token ?? '',
      from_number: props.initialValues?.config?.from_number ?? '',
      init_content_sid: props.initialValues?.config?.init_content_sid ?? '',
      content_sid: props.initialValues?.config?.content_sid ?? ''
    }
  }
})

const onSubmit = form.handleSubmit(async (values) => {
  await props.submitForm(values)
})

async function copyWebhookURL() {
  try {
    await navigator.clipboard.writeText(webhookURL.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch (_) {
    // ignore clipboard errors
  }
}
</script>
