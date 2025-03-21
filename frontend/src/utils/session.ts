import {
  IApp,
  IDataPrepChunkWithFilename,
  IDataPrepStats,
  IInteraction,
  IModelInstanceState,
  IPageBreadcrumb,
  ISession,
  ISessionMode,
  ISessionSummary,
  ISessionType,
  ITextDataPrepStage,
  SESSION_CREATOR_ASSISTANT,
  SESSION_MODE_FINETUNE,
  SESSION_MODE_INFERENCE,
  SESSION_TYPE_IMAGE,
  TEXT_DATA_PREP_DISPLAY_STAGES,
  TEXT_DATA_PREP_STAGE_NONE,
  TEXT_DATA_PREP_STAGES,
} from '../types'

import {
  getAppName,
} from './apps'

const NO_DATE = '0001-01-01T00:00:00Z'

const COLORS: Record<string, string> = {
  sdxl_inference: '#D183C9',
  sdxl_finetune: '#E3879E',
  mistral_inference: '#F4D35E',
  text_inference: '#F4D35E', // Same as mistral inference
  mistral_finetune: '#EE964B',
}

export const hasDate = (dt?: string): boolean => {
  if (!dt) return false
  return dt != NO_DATE
}

export const getUserInteraction = (session: ISession): IInteraction | undefined => {
  const userInteractions = session.interactions.filter(i => i.creator != SESSION_CREATOR_ASSISTANT)
  if (userInteractions.length <= 0) return undefined
  return userInteractions[userInteractions.length - 1]
}

export const getAssistantInteraction = (session: ISession): IInteraction | undefined => {
  const userInteractions = session.interactions.filter(i => i.creator == SESSION_CREATOR_ASSISTANT)
  if (userInteractions.length <= 0) return undefined
  return userInteractions[userInteractions.length - 1]
}

export const getFinetuneInteraction = (session: ISession): IInteraction | undefined => {
  const userInteractions = session.interactions.filter(i => {
    return i.creator == SESSION_CREATOR_ASSISTANT && i.mode == SESSION_MODE_FINETUNE
  })
  if (userInteractions.length <= 0) return undefined
  return userInteractions[userInteractions.length - 1]
}

export const hasFinishedFinetune = (session: ISession): boolean => {
  if (session.config.original_mode != SESSION_MODE_FINETUNE) return false
  const finetuneInteraction = getFinetuneInteraction(session)
  if (!finetuneInteraction) return false
  return finetuneInteraction.finished
}

export const getColor = (modelName: string, mode: ISessionMode): string => {
  // If starts with 'ollama', return inference color
  if (getModelName(modelName).indexOf('ollama') >= 0) return COLORS['text_inference']

  const key = `${getModelName(modelName)}_${mode}`
  return COLORS[key]
}

export const getModelName = (model_name: string): string => {
  if (model_name.indexOf('stabilityai') >= 0) return 'sdxl'
  if (model_name.indexOf('mistralai') >= 0) return 'mistral'
  // If has ':' in the name, it's Ollama model, need to split and keep the first part
  if (model_name.indexOf(':') >= 0) return `ollama_${model_name.split(':')[0]}`
  return ''
}

export const getHeadline = (modelName: string, mode: ISessionMode, loraDir = ''): string => {
  let loraString = ''
  if (loraDir) {
    const parts = loraDir.split('/')
    const id = parts[parts.length - 2]
    loraString = ` - ${id.split('-').pop()}`
  }
  return `${getModelName(modelName)} ${mode} ${loraString}`
}

export const getSessionHeadline = (session: ISessionSummary): string => {
  return `${getHeadline(session.model_name, session.mode, session.lora_dir)} : ${shortID(session.session_id)} : ${getTiming(session)}`
}

export const getModelInstanceNoSessionHeadline = (modelInstance: IModelInstanceState): string => {
  return `${getHeadline(modelInstance.model_name, modelInstance.mode, modelInstance.lora_dir)} : ${getModelInstanceIdleTime(modelInstance)}`
}

export const getSummaryCaption = (session: ISessionSummary): string => {
  return session.summary
}

export const getModelInstanceIdleTime = (modelInstance: IModelInstanceState): string => {
  if (!modelInstance.last_activity) return ''
  const idleFor = Date.now() - modelInstance.last_activity * 1000
  const idleForSeconds = Math.floor(idleFor / 1000)
  return `idle for ${idleForSeconds} secs, timeout is ${modelInstance.timeout} secs, stale = ${modelInstance.stale}`
}

export const shortID = (id: string): string => {
  return id.split('-').shift() || ''
}

export const getTiming = (session: ISessionSummary): string => {
  if (hasDate(session?.scheduled)) {
    const runningFor = Date.now() - new Date(session?.scheduled || '').getTime()
    const runningForSeconds = Math.floor(runningFor / 1000)
    return `${runningForSeconds} secs`
  } else if (hasDate(session?.created)) {
    const waitingFor = Date.now() - new Date(session?.created || '').getTime()
    const waitingForSeconds = Math.floor(waitingFor / 1000)
    return `${waitingForSeconds} secs`
  } else {
    return ''
  }
}

export const getSessionSummary = (session: ISession): ISessionSummary => {
  const systemInteraction = getAssistantInteraction(session)
  const userInteraction = getUserInteraction(session)
  let summary = ''
  if (session.mode == SESSION_MODE_INFERENCE) {
    summary = userInteraction?.message || ''
  } else if (session.mode == SESSION_MODE_FINETUNE) {
    summary = `fine tuning on ${userInteraction?.files.length || 0}`
  }
  return {
    session_id: session.id,
    name: session.name,
    interaction_id: systemInteraction?.id || '',
    mode: session.mode,
    type: session.type,
    model_name: session.model_name,
    owner: session.owner,
    lora_dir: session.lora_dir,
    created: systemInteraction?.created || '',
    updated: systemInteraction?.updated || '',
    scheduled: systemInteraction?.scheduled || '',
    completed: systemInteraction?.completed || '',
    summary,
  }
}

export const getTextDataPrepStageIndex = (stage: ITextDataPrepStage): number => {
  return TEXT_DATA_PREP_STAGES.indexOf(stage)
}

export const getTextDataPrepStageIndexDisplay = (stage: ITextDataPrepStage): number => {
  return TEXT_DATA_PREP_DISPLAY_STAGES.indexOf(stage)
}

export const getTextDataPrepErrors = (interaction: IInteraction): IDataPrepChunkWithFilename[] => {
  return Object.keys(interaction.data_prep_chunks || {}).reduce((acc: IDataPrepChunkWithFilename[], filename: string) => {
    const chunks = interaction.data_prep_chunks[filename]
    const errors = chunks.filter(chunk => chunk.error != '')
    if (errors.length <= 0) return acc
    return acc.concat(errors.map(error => ({ ...error, filename })))
  }, [])
}

export const getTextDataPrepStats = (interaction: IInteraction): IDataPrepStats => {
  return Object.keys(interaction.data_prep_chunks || {}).reduce((acc: IDataPrepStats, filename: string) => {
    const chunks = interaction.data_prep_chunks[filename] || []
    const errors = chunks.filter(chunk => chunk.error != '')
    const questionCount = chunks.reduce((acc: number, chunk) => acc + chunk.question_count, 0)
    return {
      total_files: acc.total_files + 1,
      total_chunks: acc.total_chunks + chunks.length,
      total_questions: acc.total_questions + questionCount,
      converted: acc.converted + (chunks.length - errors.length),
      errors: acc.errors + errors.length,
    }
  }, {
    total_files: 0,
    total_chunks: 0,
    total_questions: 0,
    converted: 0,
    errors: 0,
  })
}

/**
 * Helper function to escape special characters in a string for use in RegExp
 * This is exported for use by MessageProcessor in Markdown.tsx
 */
export function escapeRegExp(string: string): string {
  return string.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'); // $& means the whole matched string
}

export const getNewSessionBreadcrumbs = ({
  mode,
  type,
  ragEnabled,
  finetuneEnabled,
  app,
}: {
  mode: ISessionMode,
  type: ISessionType,
  ragEnabled: boolean,
  finetuneEnabled: boolean,
  app?: IApp,
}): IPageBreadcrumb[] => {

  if (mode == SESSION_MODE_FINETUNE) {
    let txt = "Add Documents"
    if (type == SESSION_TYPE_IMAGE) {
      txt += " (image style and objects)"
    } else if (ragEnabled && finetuneEnabled) {
      txt += " (hybrid RAG + Fine-tuning)"
    } else if (ragEnabled) {
      txt += " (RAG)"
    } else if (finetuneEnabled) {
      txt += " (Fine-tuning on knowledge)"
    }
    return [{
      title: txt,
    }]
  } else if (app) {
    return [{
      title: 'App Store',
      routeName: 'appstore',
    }, {
      title: getAppName(app),
    }]
  }

  return []
}