import Box from '@mui/material/Box'
import Typography from '@mui/material/Typography'
import { FC, useState } from 'react'
import { prettyBytes } from '../../utils/format'
import Cell from '../widgets/Cell'
import Row from '../widgets/Row'

import {
  ISlot
} from '../../types'

import {
  getSessionHeadline,
  getSummaryCaption
} from '../../utils/session'

export const SlotSummary: FC<{
  slot: ISlot,
  onViewSession: {
    (id: string): void,
  }
}> = ({
  slot,
  onViewSession,
}) => {

    const [historyViewing, setHistoryViewing] = useState(false)
    // const activeColor = getColor(modelInstance.model_name, modelInstance.mode)

    // const jobHistory = useMemo(() => {
    //   const history = [...modelInstance.job_history]
    //   history.reverse()
    //   return history
    // }, [
    //   modelInstance,
    // ])

    return (
      <Box
        sx={{
          width: '100%',
          p: 1,
          // border: `1px solid ${modelInstance.current_session ? activeColor : '#e5e5e5'}`,
          mt: 1,
          mb: 1,
        }}
      >
        <Row>
          <Cell>
            {/* <SessionBadge
            reverse={modelInstance.current_session ? false : true}
            modelName={modelInstance.model_name}
            mode={modelInstance.mode}
            /> */}
          </Cell>
          <Cell sx={{
            ml: 2,
          }}>
            {
              slot.current_session ? (
                <Typography
                  sx={{
                    lineHeight: 1,
                    fontWeight: 'bold',
                  }}
                  variant="body2"
                >
                  {getSessionHeadline(slot.current_session)}
                </Typography>
              ) : (
                <>
                  <Typography
                    sx={{ lineHeight: 1 }}
                    variant="body2"
                  >
                    {/* {getHeadline(slot.model_name, slot.mode, slot.lora_dir)} */}
                    {slot.id}
                  </Typography>
                  <Typography
                    sx={{ lineHeight: 1 }}
                    variant="caption"
                  >
                    {/* {getModelInstanceIdleTime(slot)} */}
                  </Typography>
                </>

              )
            }
            <Typography
              sx={{ lineHeight: 1 }}
              variant="caption"
            >
              <br /><code>{slot.status}</code>
            </Typography>
          </Cell>
          <Cell flexGrow={1} />
          <Cell>
            <Typography
              sx={{ lineHeight: 1 }}
              variant="body2"
            >
              {prettyBytes(slot.memory)}
            </Typography>
          </Cell>
        </Row>
        {
          slot.current_session && (
            <Row>
              <Cell>
                <Typography
                  sx={{ lineHeight: 1 }}
                  variant="caption"
                >
                  {getSummaryCaption(slot.current_session)}
                </Typography>
              </Cell>
              <Cell flexGrow={1} />
            </Row>
          )
        }
        <Row>
          <Cell flexGrow={1} />
        </Row>
      </Box>
    )
  }

export default SlotSummary