'use client';

import { forwardRef, useEffect, useImperativeHandle, useRef, useState, useMemo } from 'react';
import { DndContext, KeyboardSensor, PointerSensor, closestCenter, pointerWithin, useSensor, useSensors, type CollisionDetection, type DragEndEvent } from '@dnd-kit/core';
import { SortableContext, arrayMove, rectSortingStrategy, sortableKeyboardCoordinates, useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { Check, GripVertical, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { TruncatedText } from '@/components/truncated-text';
import { Popover, PopoverContent, PopoverTrigger } from './popover';

const MAX_DISPLAY = 100;
const tagClassName = 'bg-secondary text-secondary-foreground flex items-center gap-1 rounded-sm px-2 py-0.5';

type TagProps = { tag: string; onRemove: () => void };
type TagItem = { id: string; tag: string };

function identifyTags(value: string[]) {
  const occurrences = new Map<string, number>();
  return value.map((tag) => {
    const occurrence = (occurrences.get(tag) ?? 0) + 1;
    occurrences.set(tag, occurrence);
    return { id: JSON.stringify([tag, occurrence]), tag };
  });
}

const tagCollision: CollisionDetection = (args) => {
  const pointer = args.pointerCoordinates;
  if (!pointer) return closestCenter(args);
  const hits = pointerWithin(args);
  if (hits.length) return hits;
  const { x, y } = pointer;
  return closestCenter({ ...args, collisionRect: { left: x, right: x, top: y, bottom: y, width: 0, height: 0 } });
};

function RemoveTagButton({ tag, onRemove }: TagProps) {
  const { t } = useTranslation();
  return (
    <button
      type='button'
      onClick={onRemove}
      className='text-secondary-foreground/80 hover:text-secondary-foreground focus-visible:outline-foreground rounded-sm focus-visible:outline-2 focus-visible:outline-offset-1'
      aria-label={t('common.removeTag', { tag })}
    >
      <X className='size-3' />
    </button>
  );
}

function SortableTag({ id, tag, onRemove }: TagProps & { id: string }) {
  const { t } = useTranslation();
  const { attributes, listeners, setNodeRef, setActivatorNodeRef, transform, transition, isDragging } = useSortable({ id, data: { tag } });
  return (
    <div
      ref={setNodeRef}
      className={cn(tagClassName, 'max-w-full motion-reduce:transition-none!', isDragging && 'relative z-10 shadow-sm')}
      style={{ transform: CSS.Transform.toString(transform), transition }}
    >
      <button
        ref={setActivatorNodeRef}
        type='button'
        {...attributes}
        {...listeners}
        aria-label={t('common.dragToReorder', { label: tag })}
        className='focus-visible:outline-foreground flex min-w-0 cursor-grab touch-none items-center gap-1 rounded-sm text-xs select-none focus-visible:outline-2 active:cursor-grabbing'
        onClick={(event) => event.stopPropagation()}
      >
        <GripVertical aria-hidden className='text-muted-foreground size-3 shrink-0' />
        <span className='truncate' title={tag}>{tag}</span>
      </button>
      <RemoveTagButton tag={tag} onRemove={onRemove} />
    </div>
  );
}

function SortableTags({ items, onChange, onRemove }: {
  items: TagItem[];
  onChange: (items: TagItem[]) => void;
  onRemove: (index: number) => void;
}) {
  const { t } = useTranslation();
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  );
  function handleDragEnd({ active, over }: DragEndEvent) {
    if (!over || active.id === over.id) return;
    const from = items.findIndex((item) => item.id === active.id);
    const to = items.findIndex((item) => item.id === over.id);
    if (from >= 0 && to >= 0) onChange(arrayMove(items, from, to));
  }

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={tagCollision}
      onDragEnd={handleDragEnd}
      accessibility={{
        screenReaderInstructions: { draggable: t('common.tagReorder.instructions') },
        announcements: {
          onDragStart: ({ active }) => t('common.tagReorder.pickedUp', { tag: active.data.current?.tag }),
          onDragOver: ({ active, over }) => {
            if (over) {
              return t('common.tagReorder.position', { tag: active.data.current?.tag, position: over.data.current?.sortable.index + 1, total: items.length });
            }
          },
          onDragEnd: ({ active, over }) => t(over ? 'common.tagReorder.dropped' : 'common.tagReorder.cancelled', { tag: active.data.current?.tag }),
          onDragCancel: ({ active }) => t('common.tagReorder.cancelled', { tag: active.data.current?.tag }),
        },
      }}
    >
      <SortableContext items={items} strategy={rectSortingStrategy}>
        {items.map(({ id, tag }, index) => (
          <SortableTag key={id} id={id} tag={tag} onRemove={() => onRemove(index)} />
        ))}
      </SortableContext>
    </DndContext>
  );
}

interface TagsAutocompleteInputProps {
  value: string[];
  onChange: (tags: string[]) => void;
  placeholder?: string;
  className?: string;
  suggestions?: string[];
  isLoading?: boolean;
  inputLabel?: string;
  reorderable?: boolean;
}

export const TagsAutocompleteInput = forwardRef<HTMLDivElement, TagsAutocompleteInputProps>(
  ({ value = [], onChange, placeholder, className, suggestions = [], isLoading, inputLabel, reorderable }, ref) => {
    const [inputValue, setInputValue] = useState('');
    const [open, setOpen] = useState(false);
    const [isComposing, setIsComposing] = useState(false);
    const containerRef = useRef<HTMLDivElement>(null);
    const inputRef = useRef<HTMLInputElement>(null);
    useImperativeHandle(ref, () => containerRef.current!, []);
    const [editedItems, setEditedItems] = useState<TagItem[]>([]);
    const items = useMemo(() => {
      // 拖动后沿用每一枚标签的标识，让键盘焦点跟随被移动的那一枚。
      if (editedItems.length === value.length && editedItems.every((item, index) => item.tag === value[index])) return editedItems;
      return identifyTags(value);
    }, [value, editedItems]);

    const updateItems = (next: TagItem[]) => {
      setEditedItems(next);
      onChange(next.map((item) => item.tag));
    };

    // Filter suggestions based on input and not already selected (capped for performance)
    const filteredSuggestions = useMemo(() => {
      const result: string[] = [];
      const q = inputValue.trim().toLowerCase();
      for (const s of suggestions) {
        if (value.includes(s)) continue;
        if (q && !s.toLowerCase().includes(q)) continue;
        result.push(s);
        if (result.length >= MAX_DISPLAY) break;
      }
      return result;
    }, [inputValue, suggestions, value]);

    const handleInputChange = (e: React.ChangeEvent<HTMLInputElement>) => {
      setInputValue(e.target.value);
      if (e.target.value && !open && suggestions.length > 0) {
        setOpen(true);
      } else if (!e.target.value) {
        setOpen(false);
      }
    };

    const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (isComposing) return;

      if (e.key === 'Enter' || e.key === ',') {
        e.preventDefault();
        const newTag = inputValue.trim();
        if (newTag && !value.includes(newTag)) {
          onChange([...value, newTag]);
        }
        setInputValue('');
        setOpen(false);
      } else if (e.key === 'Backspace' && !inputValue && value.length > 0) {
        onChange(value.slice(0, -1));
      } else if (e.key === 'Escape') {
        setOpen(false);
      }
    };

    const removeTag = (index: number) => {
      updateItems(items.filter((_, position) => position !== index));
    };

    const handleSelectSuggestion = (suggestion: string) => {
      if (!value.includes(suggestion)) {
        onChange([...value, suggestion]);
      }
      setInputValue('');
      setOpen(false);
      inputRef.current?.focus();
    };

    const handleInputBlur = () => {
      if (isComposing) return;
      const newTag = inputValue.trim();
      if (newTag && !value.includes(newTag)) {
        onChange([...value, newTag]);
      }
      setInputValue('');
      setOpen(false);
    };

    // Focus input when clicking on the container
    const handleContainerClick = () => {
      inputRef.current?.focus();
      if (suggestions.length > 0 && inputValue) {
        setOpen(true);
      }
    };

    // Close popover when clicking outside
    useEffect(() => {
      const handleClickOutside = (e: MouseEvent) => {
        const target = e.target as HTMLElement;
        if (!target.closest('[data-tags-input-container]')) {
          setOpen(false);
        }
      };

      document.addEventListener('mousedown', handleClickOutside);
      return () => document.removeEventListener('mousedown', handleClickOutside);
    }, []);

    return (
      <div
        ref={containerRef}
        data-tags-input-container
        className={cn(
          'border-input bg-background has-[input:focus-visible]:border-ring has-[input:focus-visible]:outline-foreground flex min-h-10 w-full flex-wrap gap-1 rounded-md border px-3 py-2 text-sm has-[input:focus-visible]:outline-2 has-[input:focus-visible]:outline-offset-1',
          className
        )}
        onClick={handleContainerClick}
      >
        {reorderable ? (
          <SortableTags items={items} onChange={updateItems} onRemove={removeTag} />
        ) : (
          items.map(({ id, tag }, index) => (
            <div key={id} className={tagClassName}>
              <span className='text-xs'>{tag}</span>
              <RemoveTagButton tag={tag} onRemove={() => removeTag(index)} />
            </div>
          ))
        )}
        <Popover open={open} onOpenChange={setOpen} modal={false}>
          <PopoverTrigger asChild>
            <input
              ref={inputRef}
              aria-label={inputLabel}
              type='text'
              value={inputValue}
              onChange={handleInputChange}
              onKeyDown={handleKeyDown}
              onBlur={handleInputBlur}
              onFocus={() => {
                if (suggestions.length > 0 && inputValue) {
                  setOpen(true);
                }
              }}
              onCompositionStart={() => setIsComposing(true)}
              onCompositionEnd={() => setIsComposing(false)}
              placeholder={value.length === 0 ? placeholder : ''}
              className='placeholder:text-muted-foreground min-w-[80px] flex-1 bg-transparent outline-none'
            />
          </PopoverTrigger>
          {(isLoading || filteredSuggestions.length > 0 || inputValue.trim()) && (
            <PopoverContent
              className='w-[var(--radix-popover-trigger-width)] max-w-[var(--radix-popover-trigger-width)] p-0'
              align='start'
              onOpenAutoFocus={(e) => e.preventDefault()}
              container={containerRef.current ?? undefined}
            >
              <div className='max-h-[200px] overflow-y-auto p-1'>
                {isLoading ? (
                  <div className='text-muted-foreground p-2 text-sm'>Loading...</div>
                ) : filteredSuggestions.length > 0 ? (
                  filteredSuggestions.map((suggestion) => (
                    <div
                      key={suggestion}
                      className='hover:bg-[var(--interactive-hover)] flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm'
                      onMouseDown={(e) => {
                        e.preventDefault();
                        handleSelectSuggestion(suggestion);
                      }}
                    >
                      <Check className='text-muted-foreground h-4 w-4 shrink-0' />
                      <TruncatedText className='flex-1'>{suggestion}</TruncatedText>
                    </div>
                  ))
                ) : inputValue.trim() ? (
                  <div className='text-muted-foreground p-2 text-sm'>Press Enter to add &quot;{inputValue}&quot;</div>
                ) : null}
              </div>
            </PopoverContent>
          )}
        </Popover>
      </div>
    );
  }
);

TagsAutocompleteInput.displayName = 'TagsAutocompleteInput';
