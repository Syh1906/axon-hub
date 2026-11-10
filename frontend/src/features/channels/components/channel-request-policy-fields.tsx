import { Info } from 'lucide-react';
import { Checkbox } from '@/components/ui/checkbox';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { FormControl, FormDescription, FormField, FormItem, FormLabel, FormMessage } from '@/components/ui/form';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { useChannelActionDialog } from '../hooks/use-channel-action-dialog';

export function ChannelRequestPolicyFields({ dialog }: { dialog: ReturnType<typeof useChannelActionDialog> }) {
 const { t, form, passThroughUserAgent, setPassThroughUserAgent, userAgentInheritLabel,
  passThroughBody, setPassThroughBody, passThroughInheritLabel,
  maxSingleChannelRetriesText, setMaxSingleChannelRetriesText,
  retryableStatusCodesText, setRetryableStatusCodesText,
  retryableErrorPatternsText, setRetryableErrorPatternsText } = dialog;
 return <>
                      <FormItem className='grid grid-cols-1 items-start gap-x-6 gap-y-2 md:grid-cols-8'>
                        <FormLabel className='pt-2 font-medium md:col-span-2 md:text-right'>
                          {t('channels.dialogs.userAgentPassThrough.label')}
                        </FormLabel>
                        <div className='space-y-1 md:col-span-6'>
                          <Select
                            value={passThroughUserAgent === null ? 'inherit' : passThroughUserAgent ? 'enabled' : 'disabled'}
                            onValueChange={(value) => setPassThroughUserAgent(value === 'inherit' ? null : value === 'enabled')}
                          >
                            <SelectTrigger>
                              <SelectValue placeholder={t('channels.dialogs.userAgentPassThrough.inherit')} />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value='inherit'>{userAgentInheritLabel}</SelectItem>
                              <SelectItem value='enabled'>{t('channels.dialogs.userAgentPassThrough.enabled')}</SelectItem>
                              <SelectItem value='disabled'>{t('channels.dialogs.userAgentPassThrough.disabled')}</SelectItem>
                            </SelectContent>
                          </Select>
                        </div>
                      </FormItem>

                      <FormItem className='grid grid-cols-1 items-start gap-x-6 gap-y-2 md:grid-cols-8'>
                        <FormLabel className='pt-2 font-medium md:col-span-2 md:text-right'>
                          {t('channels.dialogs.bodyPassThrough.label')}
                        </FormLabel>
                        <div className='space-y-2 md:col-span-6'>
                          <Select
                            value={passThroughBody === null ? 'inherit' : passThroughBody ? 'enabled' : 'disabled'}
                            onValueChange={(value) => setPassThroughBody(value === 'inherit' ? null : value === 'enabled')}
                          >
                            <SelectTrigger>
                              <SelectValue placeholder={t('channels.dialogs.bodyPassThrough.inherit')} />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value='inherit'>{passThroughInheritLabel}</SelectItem>
                              <SelectItem value='enabled'>{t('channels.dialogs.bodyPassThrough.enabled')}</SelectItem>
                              <SelectItem value='disabled'>{t('channels.dialogs.bodyPassThrough.disabled')}</SelectItem>
                            </SelectContent>
                          </Select>
                          {passThroughBody === true && (
                            <p className='text-xs text-amber-600 dark:text-amber-400'>{t('channels.dialogs.bodyPassThrough.warning')}</p>
                          )}
                        </div>
                      </FormItem>

                      <FormField
                        control={form.control}
                        name='settings.forceOneHourPromptCache'
                        render={({ field }) => (
                          <FormItem className='grid grid-cols-1 items-start gap-x-6 gap-y-2 md:grid-cols-8'>
                            <FormLabel className='pt-2 font-medium md:col-span-2 md:text-right'>
                              {t('channels.dialogs.forceOneHourPromptCache.label')}
                            </FormLabel>
                            <div className='space-y-1 md:col-span-6'>
                              <FormControl>
                                <Checkbox checked={field.value ?? false} onCheckedChange={(checked) => field.onChange(checked === true)} />
                              </FormControl>
                              <FormDescription>{t('channels.dialogs.forceOneHourPromptCache.description')}</FormDescription>
                              <FormMessage />
                            </div>
                          </FormItem>
                        )}
                      />

                      <FormField
                        control={form.control}
                        name='settings.healthGateFailureThreshold'
                        render={({ field }) => (
                          <FormItem className='grid grid-cols-1 items-start gap-x-6 gap-y-2 md:grid-cols-8'>
                            <FormLabel className='pt-2 font-medium md:col-span-2 md:text-right'>
                              {t('channels.dialogs.healthGate.failureThreshold')}
                            </FormLabel>
                            <div className='space-y-1 md:col-span-6'>
                              <FormControl>
                                <Input
                                  type='number'
                                  min='0'
                                  step='1'
                                  value={field.value ?? ''}
                                  onChange={(event) => field.onChange(event.target.value === '' ? null : Number(event.target.value))}
                                  placeholder={t('channels.dialogs.healthGate.inherit')}
                                />
                              </FormControl>
                              <FormDescription>{t('channels.dialogs.healthGate.overrideDescription')}</FormDescription>
                              <FormMessage />
                            </div>
                          </FormItem>
                        )}
                      />

                      <FormItem className='grid grid-cols-1 items-start gap-x-6 gap-y-2 md:grid-cols-8'>
                        <div className='flex items-center gap-1.5 pt-2 md:col-span-2 md:justify-start'>
                          <FormLabel className='font-medium whitespace-nowrap'>
                            {t('channels.dialogs.maxSingleChannelRetries.label')}
                          </FormLabel>
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <button
                                type='button'
                                className='text-muted-foreground hover:text-foreground inline-flex items-center'
                                aria-label={t('channels.dialogs.maxSingleChannelRetries.tooltip')}
                              >
                                <Info className='h-3.5 w-3.5' />
                              </button>
                            </TooltipTrigger>
                            <TooltipContent className='max-w-sm'>
                              <p>{t('channels.dialogs.maxSingleChannelRetries.tooltip')}</p>
                            </TooltipContent>
                          </Tooltip>
                        </div>
                        <div className='md:col-span-6'>
                          <Input
                            value={maxSingleChannelRetriesText}
                            onChange={(event) => setMaxSingleChannelRetriesText(event.target.value)}
                            placeholder={t('channels.dialogs.maxSingleChannelRetries.placeholder')}
                            className='font-mono text-sm'
                          />
                        </div>
                      </FormItem>

                      <FormItem className='grid grid-cols-1 items-start gap-x-6 gap-y-2 md:grid-cols-8'>
                        <div className='flex items-center gap-1.5 pt-2 md:col-span-2 md:justify-start'>
                          <FormLabel className='font-medium'>{t('channels.dialogs.retryableStatusCodes.label')}</FormLabel>
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <button
                                type='button'
                                className='text-muted-foreground hover:text-foreground inline-flex items-center'
                                aria-label={t('channels.dialogs.retryableStatusCodes.tooltip')}
                              >
                                <Info className='h-3.5 w-3.5' />
                              </button>
                            </TooltipTrigger>
                            <TooltipContent className='max-w-sm'>
                              <p>{t('channels.dialogs.retryableStatusCodes.tooltip')}</p>
                            </TooltipContent>
                          </Tooltip>
                        </div>
                        <div className='md:col-span-6'>
                          <Input
                            value={retryableStatusCodesText}
                            onChange={(event) => setRetryableStatusCodesText(event.target.value)}
                            placeholder={t('channels.dialogs.retryableStatusCodes.placeholder')}
                            className='font-mono text-sm'
                          />
                        </div>
                      </FormItem>

                      <FormItem className='grid grid-cols-1 items-start gap-x-6 gap-y-2 md:grid-cols-8'>
                        <div className='flex items-center gap-1.5 pt-2 md:col-span-2 md:justify-start'>
                          <FormLabel className='font-medium'>{t('channels.dialogs.retryableErrorPatterns.label')}</FormLabel>
                          <Tooltip>
                            <TooltipTrigger asChild>
                              <button
                                type='button'
                                className='text-muted-foreground hover:text-foreground inline-flex items-center'
                                aria-label={t('channels.dialogs.retryableErrorPatterns.description')}
                              >
                                <Info className='h-3.5 w-3.5' />
                              </button>
                            </TooltipTrigger>
                            <TooltipContent className='max-w-sm'>
                              <p>{t('channels.dialogs.retryableErrorPatterns.description')}</p>
                            </TooltipContent>
                          </Tooltip>
                        </div>
                        <div className='md:col-span-6'>
                          <Textarea
                            value={retryableErrorPatternsText}
                            onChange={(event) => setRetryableErrorPatternsText(event.target.value)}
                            placeholder={t('channels.dialogs.retryableErrorPatterns.placeholder')}
                            className='min-h-[88px] resize-y font-mono text-sm'
                          />
                        </div>
                      </FormItem>



 </>;
}
