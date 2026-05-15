import * as z from 'zod'

export const createFormSchema = (t) =>
  z.object({
    name: z.string().min(1, t('globals.messages.required')),
    enabled: z.boolean().optional(),
    csat_enabled: z.boolean().optional(),
    prompt_tags_on_reply: z.boolean().optional(),
    config: z.object({
      account_sid: z.string().min(1, t('globals.messages.required')),
      auth_token: z.string().min(1, t('globals.messages.required')),
      from_number: z
        .string()
        .min(1, t('globals.messages.required'))
        .regex(/^\+[1-9]\d{6,14}$/, t('validation.invalidValue'))
    })
  })
