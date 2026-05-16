<template>
  <div class="mb-5">
    <CustomBreadcrumb :links="breadcrumbLinks" />
  </div>
  <Spinner v-if="formLoading"></Spinner>
  <div v-else>
    <EmailInboxForm
      :initialValues="inbox"
      :submitForm="submitForm"
      :isLoading="isLoading"
      v-if="inbox.channel === 'email'"
    />
    <LivechatInboxForm
      :initialValues="inbox"
      :submitForm="submitForm"
      :isLoading="isLoading"
      :available-languages="availableLanguages"
      v-else-if="inbox.channel === 'livechat'"
    />
    <WhatsAppInboxForm
      :initialValues="inbox"
      :inboxUUID="inbox.uuid"
      :submitForm="submitForm"
      :isLoading="isLoading"
      v-else-if="inbox.channel === 'whatsapp'"
    />
  </div>
</template>

<script setup>
import { onMounted, ref } from 'vue'
import api from '../../../api'
import EmailInboxForm from '@/features/admin/inbox/EmailInboxForm.vue'
import LivechatInboxForm from '@/features/admin/inbox/LivechatInboxForm.vue'
import WhatsAppInboxForm from '@/features/admin/inbox/WhatsAppInboxForm.vue'
import { CustomBreadcrumb } from '@shared-ui/components/ui/breadcrumb/index.js'
import { Spinner } from '@shared-ui/components/ui/spinner'
import { EMITTER_EVENTS } from '@/constants/emitterEvents.js'
import { AUTH_TYPE_PASSWORD, AUTH_TYPE_OAUTH2 } from '@/constants/auth.js'
import { useEmitter } from '@/composables/useEmitter'
import { handleHTTPError } from '@shared-ui/utils/http.js'
import { useI18n } from 'vue-i18n'

const emitter = useEmitter()
const { t } = useI18n()
const formLoading = ref(false)
const isLoading = ref(false)
const inbox = ref({})
const availableLanguages = ref([])
const breadcrumbLinks = [
  { path: 'inbox-list', label: t('globals.terms.inbox', 2) },
  { path: '', label: t('inbox.edit') }
]

const submitForm = (values) => {
  let payload

  if (inbox.value.channel === 'email') {
    const config = {
      auth_type: values.auth_type,
      reply_to: values.reply_to,
      enable_plus_addressing: values.enable_plus_addressing,
      imap: [{ ...values.imap }],
      smtp: [{ ...values.smtp }]
    }

    if (values.auth_type === AUTH_TYPE_OAUTH2) {
      config.oauth = values.oauth
    }

    payload = {
      ...values,
      channel: inbox.value.channel,
      config
    }

    if (payload.config.imap[0].password?.includes('•')) {
      payload.config.imap[0].password = ''
    }

    if (payload.config.auth_type === AUTH_TYPE_OAUTH2) {
      if (payload.config.oauth.access_token?.includes('•')) {
        payload.config.oauth.access_token = ''
      }
      if (payload.config.oauth.client_secret?.includes('•')) {
        payload.config.oauth.client_secret = ''
      }
      if (payload.config.oauth.refresh_token?.includes('•')) {
        payload.config.oauth.refresh_token = ''
      }
    }

    payload.config.smtp.forEach((smtp) => {
      if (smtp.password?.includes('•')) {
        smtp.password = ''
      }
    })
  } else if (inbox.value.channel === 'livechat') {
    payload = {
      ...values,
      channel: inbox.value.channel,
      config: values.config
    }
  } else if (inbox.value.channel === 'whatsapp') {
    const config = {
      account_sid: values.config.account_sid,
      auth_token: values.config.auth_token,
      from_number: values.config.from_number,
      content_sid: values.config.content_sid ?? ''
    }
    // Skip masked auth_token (unchanged)
    if (config.auth_token?.includes('•')) {
      config.auth_token = ''
    }
    payload = {
      ...values,
      channel: inbox.value.channel,
      config
    }
  }

  updateInbox(payload)
}
const updateInbox = async (payload) => {
  try {
    isLoading.value = true
    await api.updateInbox(inbox.value.id, payload)
    emitter.emit(EMITTER_EVENTS.SHOW_TOAST, {
      description: t('globals.messages.savedSuccessfully')
    })
  } catch (error) {
    emitter.emit(EMITTER_EVENTS.SHOW_TOAST, {
      variant: 'destructive',
      description: handleHTTPError(error).message
    })
  } finally {
    isLoading.value = false
  }
}

onMounted(async () => {
  try {
    formLoading.value = true
    const [resp, langsResp] = await Promise.all([
      api.getInbox(props.id),
      api.getAvailableLanguages()
    ])
    availableLanguages.value = langsResp.data.data
    let inboxData = resp.data.data

    // Modify the inbox data as per the zod schema.
    if (inboxData?.config?.imap) {
      inboxData.imap = inboxData?.config?.imap[0]
    }
    if (inboxData?.config?.smtp) {
      inboxData.smtp = inboxData?.config?.smtp[0]
    }
    inboxData.auth_type = inboxData?.config?.auth_type || AUTH_TYPE_PASSWORD
    inboxData.oauth = inboxData?.config?.oauth || {}
    inboxData.enable_plus_addressing = inboxData?.config?.enable_plus_addressing || false
    inboxData.reply_to = inboxData?.config?.reply_to || ''
    // Flatten whatsapp config for the form.
    if (inboxData.channel === 'whatsapp') {
      inboxData.config = {
        account_sid: inboxData?.config?.account_sid || '',
        auth_token: inboxData?.config?.auth_token || '',
        from_number: inboxData?.config?.from_number || ''
      }
    }
    inbox.value = inboxData
  } catch (error) {
    emitter.emit(EMITTER_EVENTS.SHOW_TOAST, {
      variant: 'destructive',
      description: handleHTTPError(error).message
    })
  } finally {
    formLoading.value = false
  }
})

const props = defineProps({
  id: {
    type: String,
    required: true
  }
})
</script>
